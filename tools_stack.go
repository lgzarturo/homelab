package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// serviciosStack son los servicios de compose.yml: la lista cerrada que acepta logs_servicio.
// El primero es Ollama; `up --lab` detiene los demás.
var serviciosStack = []string{"ollama", "open-webui", "speaches", "n8n", "filebrowser", "portainer"}

const maxLogs = 3000 // caracteres de log que recibe el modelo

// leerCompose ejecuta un subcomando de solo lectura de docker compose en la carpeta del homelab.
func leerCompose(dir string, args ...string) (string, error) {
	if !esDelHomelab(filepath.Join(dir, "compose.yml")) {
		return "", errors.New("no hay un homelab en marcha en esta carpeta")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no pude consultar Docker: %v", err)
	}
	return string(out), nil
}

func servicios(dir string) (any, error) {
	out, err := leerCompose(dir, "ps", "-a", "--format", "{{.Service}}\t{{.State}}\t{{.Status}}")
	if err != nil {
		return nil, err
	}
	lista := []map[string]string{}
	for _, linea := range strings.Split(strings.TrimSpace(out), "\n") {
		if campos := strings.Split(linea, "\t"); len(campos) == 3 {
			lista = append(lista, map[string]string{"servicio": campos[0], "estado": campos[1], "detalle": campos[2]})
		}
	}
	return map[string]any{"servicios": lista}, nil
}

func logsServicio(dir string, args map[string]any) (any, error) {
	servicio, _ := args["servicio"].(string)
	if !slices.Contains(serviciosStack, servicio) { // lista cerrada: nunca llega texto libre a la línea de comandos
		return nil, fmt.Errorf("servicio desconocido: usa uno de %s", strings.Join(serviciosStack, ", "))
	}
	out, err := leerCompose(dir, "logs", "--no-color", "--no-log-prefix", "--tail", "30", servicio)
	if err != nil {
		return nil, err
	}
	if r := []rune(out); len(r) > maxLogs {
		out = "…" + string(r[len(r)-maxLogs:])
	}
	return map[string]string{"servicio": servicio, "ultimas_lineas": strings.TrimSpace(out)}, nil
}

// procesos devuelve solo nombre y memoria: nunca usuarios, rutas ni argumentos.
func procesos() (any, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, err
	}
	porNombre := map[string]uint64{}
	for _, p := range ps {
		nombre, err := p.Name()
		if err != nil || nombre == "" {
			continue
		}
		if m, err := p.MemoryInfo(); err == nil && m != nil {
			porNombre[nombre] += m.RSS
		}
	}
	type fila struct {
		Nombre    string `json:"nombre"`
		MemoriaMB uint64 `json:"memoria_mb"`
	}
	filas := make([]fila, 0, len(porNombre))
	for n, b := range porNombre {
		filas = append(filas, fila{n, b >> 20})
	}
	slices.SortFunc(filas, func(a, b fila) int {
		return cmp.Or(cmp.Compare(b.MemoriaMB, a.MemoriaMB), cmp.Compare(a.Nombre, b.Nombre))
	})
	return map[string]any{"procesos": filas[:min(5, len(filas))]}, nil
}

func leerNotas(dir string) (any, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	type nota struct {
		Archivo string `json:"archivo"`
		Texto   string `json:"texto"`
		fecha   time.Time
	}
	notas := []nota{}
	for _, e := range entradas {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		notas = append(notas, nota{Archivo: e.Name(), fecha: info.ModTime()})
	}
	slices.SortFunc(notas, func(a, b nota) int { return cmp.Or(b.fecha.Compare(a.fecha), cmp.Compare(b.Archivo, a.Archivo)) })
	notas = notas[:min(5, len(notas))]
	for i := range notas {
		if b, err := os.ReadFile(filepath.Join(dir, notas[i].Archivo)); err == nil {
			notas[i].Texto = recortar(string(b), 600)
		}
	}
	return map[string]any{"notas": notas}, nil
}
