package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// bytesDe convierte tamaños de Docker como "6g", "512m" o "3gb" a bytes; 0 si no se entiende.
func bytesDe(s string) int64 {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "b")
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k':
		mult = 1 << 10
	case 'm':
		mult = 1 << 20
	case 'g':
		mult = 1 << 30
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0
	}
	return int64(n * float64(mult))
}

// dockerVivo comprueba que el motor de Docker responde y devuelve los núcleos y la memoria que ofrece
// a los contenedores (en Mac y Windows, los de su máquina virtual).
func dockerVivo() (cpus int, memoria int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.NCPU}} {{.MemTotal}}").Output()
	if err == nil {
		_, err = fmt.Sscan(string(out), &cpus, &memoria)
	}
	if err != nil || cpus == 0 {
		return 0, 0, errors.New("Docker no está corriendo: abre Docker Desktop (en Linux, inicia el servicio docker) y vuelve a intentar")
	}
	return cpus, memoria, nil
}

// limiteOllama es la memoria máxima de Ollama: la del contenedor si existe (puede diferir de .env),
// si no la de OLLAMA_MEM. 0 = sin límite conocido (Ollama nativo).
func limiteOllama(env map[string]string) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.HostConfig.Memory}}", "ia-local").Output(); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if !perfilActivo(env, "ollama") {
		return 0
	}
	return bytesDe(valor(env, "OLLAMA_MEM", "6g"))
}

// cabe dice si un modelo de ese peso entra en la memoria de Ollama: "sí", "justo" (menos de 15 % de
// margen para el contexto) o "no". Sin límite o sin peso conocido responde "sí".
func cabe(peso, limite int64) string {
	switch {
	case limite <= 0 || peso <= 0:
		return "sí"
	case peso > limite:
		return "no"
	case float64(peso)*1.15 > float64(limite):
		return "justo"
	}
	return "sí"
}

// presupuesto suma los límites de memoria de los servicios activos (los que no tienen límite se
// estiman en 0.6 GB entre todos).
func presupuesto(env map[string]string) int64 {
	var total int64
	for _, s := range []struct{ perfil, clave, porDefecto string }{
		{"ollama", "OLLAMA_MEM", "6g"},
		{"openwebui", "OPENWEBUI_MEM", "2g"},
		{"speaches", "SPEACHES_MEM", "3g"},
	} {
		if perfilActivo(env, s.perfil) {
			total += bytesDe(valor(env, s.clave, s.porDefecto))
		}
	}
	if perfilActivo(env, "n8n") || perfilActivo(env, "filebrowser") || perfilActivo(env, "portainer") {
		total += 600 << 20
	}
	return total
}

var puertos = []struct {
	perfil    string
	puerto    int
	soloLocal bool
}{
	{"ollama", 11434, true}, {"openwebui", 3000, false}, {"n8n", 5678, false},
	{"filebrowser", 8080, false}, {"portainer", 9443, false}, {"speaches", 8000, true},
}

// puertosOcupados prueba a escuchar en cada puerto de los servicios activos.
func puertosOcupados(env map[string]string) []int {
	var ocupados []int
	for _, p := range puertos {
		if !perfilActivo(env, p.perfil) {
			continue
		}
		ip := valor(env, "BIND_IP", "127.0.0.1")
		if p.soloLocal {
			ip = "127.0.0.1"
		}
		l, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(p.puerto)))
		if err != nil {
			ocupados = append(ocupados, p.puerto)
			continue
		}
		l.Close()
	}
	return ocupados
}

// stackCorriendo indica si hay contenedores del homelab en marcha (entonces los puertos son suyos).
func stackCorriendo(dir string) bool {
	cmd := exec.Command("docker", "compose", "ps", "-q")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func cmdDoctor(flagDir string, _ []string) error {
	fallos, avisos := 0, 0
	bien := func(s string) { fmt.Println(verde("  ✓ ") + s) }
	aviso := func(s string) { avisos++; fmt.Println(limon("  ⚠ ") + s) }
	mal := func(s string) { fallos++; fmt.Println(rojo("  ✗ " + s)) }

	dir, err := resolverDir(flagDir)
	if err != nil {
		return err
	}
	fmt.Println(morado("🩺 Doctor") + gris(" · "+dir))

	// Docker
	var cpus int
	var memDocker int64
	docker := false
	if _, err := exec.LookPath("docker"); err != nil {
		mal("No encuentro Docker: instálalo (Docker Desktop en Mac/Windows)")
	} else if cpus, memDocker, err = dockerVivo(); err != nil {
		mal(err.Error())
	} else {
		docker = true
		bien(fmt.Sprintf("Docker responde: %d núcleos y %s para contenedores", cpus, gb(memDocker)))
	}

	// Carpeta
	if !esDelHomelab(filepath.Join(dir, "compose.yml")) {
		aviso("Aún no hay homelab en esta carpeta: corre `homelab up` (o usa --dir)")
		return veredicto(fallos, avisos)
	}
	notas := filepath.Join(dir, "archivos", "notas")
	if f, err := os.CreateTemp(notas, ".doctor-*"); err != nil {
		mal("No puedo escribir en " + notas + ". En Windows suele ser el acceso controlado a carpetas de Defender: permite homelab.exe (README, «Problemas comunes»)")
	} else {
		f.Close()
		os.Remove(f.Name())
		bien("La carpeta permite escribir (notas del agente)")
	}
	env := leerEnv(dir)

	// Memoria y núcleos
	if docker {
		if total := presupuesto(env); total > memDocker {
			aviso(fmt.Sprintf("Los límites de memoria suman %s y Docker tiene %s: baja OLLAMA_MEM, quita servicios de COMPOSE_PROFILES o dale más memoria a Docker", gb(total), gb(memDocker)))
		} else {
			bien(fmt.Sprintf("Memoria: los límites suman %s de %s", gb(total), gb(memDocker)))
		}
		if n := entero(env["OLLAMA_CPUS"], 2); perfilActivo(env, "ollama") && n > cpus {
			mal(fmt.Sprintf("OLLAMA_CPUS=%d pero Docker solo tiene %d núcleos: `homelab up` fallará", n, cpus))
		}
	}

	// Puertos (solo con el homelab apagado: encendido, los puertos son suyos)
	if docker && !stackCorriendo(dir) {
		if oc := puertosOcupados(env); len(oc) > 0 {
			mal(fmt.Sprintf("Puertos ocupados por otro programa: %v", oc))
		} else {
			bien("Los puertos del homelab están libres")
		}
	}

	// Ollama y modelos
	o := nuevoOllama()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := o.Version(ctx)
	if err != nil {
		aviso("Ollama no responde en " + o.URL + ": corre `homelab up`")
		return veredicto(fallos, avisos)
	}
	ms, _ := o.Tags(ctx)
	bien(fmt.Sprintf("Ollama %s responde con %d modelos", v, len(ms)))
	limite := limiteOllama(env)
	for _, m := range ms {
		switch cabe(m.Size, limite) {
		case "no":
			aviso(fmt.Sprintf("%s no cabe: pesa %s y Ollama tiene %s", m.Name, gb(m.Size), gb(limite)))
		case "justo":
			aviso(fmt.Sprintf("%s cabe justo: pesa %s de %s; con contextos largos puede fallar", m.Name, gb(m.Size), gb(limite)))
		}
	}
	return veredicto(fallos, avisos)
}

func veredicto(fallos, avisos int) error {
	fmt.Println()
	switch {
	case fallos > 0:
		return fmt.Errorf("%d problema(s) que corregir y %d aviso(s)", fallos, avisos)
	case avisos > 0:
		fmt.Println(limon(fmt.Sprintf("  Funciona, con %d aviso(s).", avisos)))
	default:
		fmt.Println(verde("  Todo en orden."))
	}
	return nil
}
