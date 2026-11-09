package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

//go:embed compose.yml
var composeYML []byte

//go:embed .env.example
var envEjemplo []byte

//go:embed filebrowser/config/settings.json
var filebrowserSettings []byte

// marcaHomelab reconoce un compose.yml propio: nunca se pisa ni se apaga el proyecto Docker de otra app.
var marcaHomelab = regexp.MustCompile(`(?m)^name: homelab\s*$`)

func esDelHomelab(rutaCompose string) bool {
	b, err := os.ReadFile(rutaCompose)
	return err == nil && marcaHomelab.Match(b)
}

// resolverDir elige la carpeta del homelab: --dir, HOMELAB_DIR, la carpeta actual si tiene el
// compose.yml del homelab, o ~/homelab.
func resolverDir(flagDir string) (string, error) {
	if flagDir != "" {
		return filepath.Abs(flagDir)
	}
	if d := os.Getenv("HOMELAB_DIR"); d != "" {
		return filepath.Abs(d)
	}
	if wd, err := os.Getwd(); err == nil && esDelHomelab(filepath.Join(wd, "compose.yml")) {
		return wd, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "homelab"), nil
}

// prepararDir deja la carpeta lista para docker compose. compose.yml sale del binario (la versión
// anterior queda en compose.yml.bak; personaliza con compose.override.yml) y se niega a pisar un
// compose.yml ajeno. .env y settings.json solo se crean si faltan.
func prepararDir(dir string) (composeActualizado bool, err error) {
	ruta := filepath.Join(dir, "compose.yml")
	actual, err := os.ReadFile(ruta)
	existe := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if existe && !marcaHomelab.Match(actual) {
		return false, fmt.Errorf("%s ya tiene un compose.yml de otro proyecto: usa otra carpeta con --dir", dir)
	}
	for _, d := range []string{
		filepath.Join(dir, "archivos", "notas"),
		filepath.Join(dir, "filebrowser", "database"),
		filepath.Join(dir, "filebrowser", "config"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return false, err
		}
	}
	if !existe || !bytes.Equal(actual, composeYML) {
		if existe {
			if err := os.WriteFile(ruta+".bak", actual, 0o644); err != nil {
				return false, err
			}
		}
		if err := os.WriteFile(ruta, composeYML, 0o644); err != nil {
			return false, err
		}
		composeActualizado = true
	}
	env := envEjemplo
	if _, err := os.Stat(filepath.Join(dir, "modelos", "models")); err == nil {
		// Ya hay modelos en ./modelos (instalación anterior): se reutilizan en vez de descargarlos otra vez.
		env = bytes.Replace(env, []byte("OLLAMA_DATA=ollama_data"), []byte("OLLAMA_DATA=./modelos"), 1)
	}
	if err := escribirSiFalta(filepath.Join(dir, ".env"), env); err != nil {
		return composeActualizado, err
	}
	return composeActualizado, escribirSiFalta(filepath.Join(dir, "filebrowser", "config", "settings.json"), filebrowserSettings)
}

func escribirSiFalta(ruta string, datos []byte) error {
	f, err := os.OpenFile(ruta, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Write(datos)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// leerEnv lee el .env de la carpeta (CLAVE=valor; ignora comentarios y líneas vacías).
func leerEnv(dir string) map[string]string {
	env := map[string]string{}
	datos, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return env
	}
	for _, linea := range strings.Split(string(datos), "\n") {
		linea = strings.TrimSpace(linea)
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		k, v, ok := strings.Cut(linea, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
			v = strings.Trim(v, `"'`)
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i]) // comentario al final de la línea, como lo entiende docker compose
		}
		env[strings.TrimSpace(k)] = v
	}
	return env
}

func valor(env map[string]string, clave, porDefecto string) string {
	if v := env[clave]; v != "" {
		return v
	}
	return porDefecto
}

// entero lee números como "2" o "1.5" (OLLAMA_CPUS admite fracciones) y devuelve la parte entera.
func entero(s string, porDefecto int) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return porDefecto
	}
	return int(f)
}

// lista separa valores por coma: "a, b,,c" -> [a b c].
func lista(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func perfilActivo(env map[string]string, perfil string) bool {
	for _, p := range lista(env["COMPOSE_PROFILES"]) {
		if p == perfil {
			return true
		}
	}
	return false
}

// compose ejecuta docker compose dentro de la carpeta (carga compose.yml, compose.override.yml y .env).
func compose(dir string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("no encuentro Docker: instálalo (Docker Desktop en Mac/Windows) y vuelve a intentar")
	}
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s falló: %w", args[0], err)
	}
	return nil
}

// dirExistente resuelve la carpeta y exige que ya tenga un homelab.
func dirExistente(flagDir string) (string, error) {
	dir, err := resolverDir(flagDir)
	if err != nil {
		return "", err
	}
	if !esDelHomelab(filepath.Join(dir, "compose.yml")) {
		return "", fmt.Errorf("no hay homelab en %s: corre `homelab up` (o usa --dir)", dir)
	}
	return dir, nil
}

func cmdUp(flagDir string, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	sinModelos := fs.Bool("sin-modelos", false, "no descargar modelos de IA ni de voz")
	if _, err := parsear(fs, args); err != nil {
		return err
	}
	dir, err := resolverDir(flagDir)
	if err != nil {
		return err
	}
	actualizado, err := prepararDir(dir)
	if err != nil {
		return fmt.Errorf("no pude preparar %s: %w", dir, err)
	}
	fmt.Println(morado("🌱 Homelab") + gris(" · "+dir))
	if actualizado {
		fmt.Println(gris("  compose.yml escrito desde el binario (si había uno, quedó en compose.yml.bak; tus ajustes van en .env o compose.override.yml)"))
	}
	env := leerEnv(dir)
	if err := compose(dir, "up", "-d"); err != nil {
		return err
	}
	o := nuevoOllama()
	if perfilActivo(env, "ollama") {
		if err := esperarOllama(o, 2*time.Minute); err != nil {
			return err
		}
	}
	if !*sinModelos {
		if err := instalarModelos(o, lista(env["OLLAMA_MODELS"])); err != nil {
			if perfilActivo(env, "ollama") {
				return err
			}
			fmt.Println(gris("  ⚠ " + err.Error()))
		}
		if perfilActivo(env, "speaches") {
			descargarVoz(env)
		}
	}
	imprimirURLs(env)
	return nil
}

func cmdDown(flagDir string, _ []string) error {
	dir, err := dirExistente(flagDir)
	if err != nil {
		return err
	}
	return compose(dir, "down")
}

func cmdLogs(flagDir string, args []string) error {
	dir, err := dirExistente(flagDir)
	if err != nil {
		return err
	}
	return compose(dir, append([]string{"logs", "--tail", "100"}, args...)...)
}

func cmdStatus(flagDir string, _ []string) error {
	dir, err := dirExistente(flagDir)
	if err != nil {
		return err
	}
	if err := compose(dir, "ps", "--format", "table {{.Service}}\t{{.Status}}"); err != nil {
		return err
	}
	fmt.Println()
	o := nuevoOllama()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := o.Version(ctx)
	if err != nil {
		fmt.Println(rojo("  ✗ " + err.Error()))
	} else {
		ms, _ := o.Tags(ctx)
		cargado := "ninguno en memoria"
		if ps, err := o.PS(ctx); err == nil && len(ps) > 0 {
			p := ps[0]
			cargado = fmt.Sprintf("en memoria: %s (%s, se libera en %s)", p.Name, gb(p.Size), time.Until(p.ExpiresAt).Round(time.Minute))
		}
		fmt.Printf("  Ollama %s · %d modelos instalados · %s\n", v, len(ms), cargado)
	}
	imprimirURLs(leerEnv(dir))
	return nil
}

func esperarOllama(o *Ollama, limite time.Duration) error {
	r := girar("esperando a Ollama")
	defer r.parar()
	fin := time.Now().Add(limite)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := o.Version(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(fin) {
			return fmt.Errorf("Ollama no respondió en %s: revisa `homelab logs ollama`", limite)
		}
		time.Sleep(time.Second)
	}
}

// hostLocal es la dirección desde la que este equipo llega a los puertos publicados.
func hostLocal(env map[string]string) string {
	switch b := env["BIND_IP"]; b {
	case "", "0.0.0.0", "127.0.0.1":
		return "127.0.0.1"
	default:
		return b
	}
}

// descargarVoz baja los modelos de Whisper y de voz a speaches para poder trabajar sin internet después.
func descargarVoz(env map[string]string) {
	base := "http://127.0.0.1:8000" // speaches solo se publica en localhost
	if err := esperarURL(base+"/health", 2*time.Minute); err != nil {
		fmt.Println(gris("  ⚠ speaches no respondió; los modelos de voz se bajarán la primera vez que los uses"))
		return
	}
	for _, m := range []string{
		valor(env, "STT_MODEL", "Systran/faster-whisper-small"),
		valor(env, "TTS_MODEL", "speaches-ai/Kokoro-82M-v1.0-ONNX"),
	} {
		r := girar("preparando voz " + m)
		err := descargarModeloVoz(base, m)
		r.parar()
		if err != nil {
			fmt.Println(gris("  ⚠ voz: " + err.Error()))
			continue
		}
		fmt.Println(verde("  ✓ ") + m)
	}
}

func descargarModeloVoz(base, modelo string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/models/"+modelo, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 200: descargado ahora · 201: ya estaba
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("%s: speaches respondió %d", modelo, resp.StatusCode)
	}
	return nil
}

func esperarURL(url string, limite time.Duration) error {
	fin := time.Now().Add(limite)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		cancel()
		if err == nil && resp.StatusCode == http.StatusOK {
			return nil
		}
		if time.Now().After(fin) {
			return fmt.Errorf("%s no respondió", url)
		}
		time.Sleep(time.Second)
	}
}

var paneles = []struct {
	perfil, nombre, url string
	soloLocal           bool // publicado siempre en 127.0.0.1, sin importar BIND_IP
}{
	{"openwebui", "Open WebUI · chat, voz y documentos", "http://%s:3000", false},
	{"n8n", "n8n · automatizaciones", "http://%s:5678", false},
	{"filebrowser", "Filebrowser · archivos y notas", "http://%s:8080", false},
	{"portainer", "Portainer · Docker", "https://%s:9443", false},
	{"speaches", "Speaches · API de voz", "http://%s:8000/docs", true},
}

func imprimirURLs(env map[string]string) {
	host := hostLocal(env)
	if host == "127.0.0.1" {
		host = "localhost"
	}
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	for _, p := range paneles {
		if !perfilActivo(env, p.perfil) {
			continue
		}
		h := host
		if p.soloLocal {
			h = "localhost"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", p.nombre, fmt.Sprintf(p.url, h))
	}
	fmt.Fprintf(tw, "  Ollama · API local\t%s\n", nuevoOllama().URL)
	tw.Flush()
	if host == "localhost" {
		fmt.Println(gris("  Solo se entra desde este equipo. Para abrirlo a tu red local: BIND_IP=0.0.0.0 en .env y `homelab up`."))
		return
	}
	if env["BIND_IP"] == "0.0.0.0" {
		fmt.Println(gris("  Desde otros equipos de tu red usa la IP de este equipo en lugar de localhost."))
	}
	fmt.Println(rojo("  ⚠ Los paneles están abiertos a tu red: entra ya y crea las cuentas de administrador (el primero que entra se queda con ellas)."))
	if perfilActivo(env, "portainer") {
		fmt.Println(rojo("  ⚠ Portainer monta docker.sock: quien lo controle controla este equipo. Quítalo de COMPOSE_PROFILES si no lo usas."))
	}
}
