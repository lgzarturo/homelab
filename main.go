// homelab: levanta un homelab de IA local con Docker, conversa con los modelos y compara su rendimiento.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

var version = "dev"

const ayuda = `homelab · IA local con Docker

Uso:
  homelab <comando> [opciones]

Comandos:
  up [--sin-modelos]        Levanta los servicios y descarga los modelos que falten
  down                      Detiene los servicios
  status                    Estado de los servicios, de Ollama y URLs
  logs [servicio] [-f]      Muestra los logs (docker compose logs)
  models                    Modelos instalados y sus capacidades
  pull <modelo>...          Descarga modelos de Ollama
  chat [modelo] [--pensar]  Conversa con el agente local (/ayuda dentro del chat)
  version                   Muestra la versión

Opciones globales:
  --dir <ruta>              Carpeta del homelab (también HOMELAB_DIR). Por defecto: la
                            carpeta actual si tiene compose.yml; si no, ~/homelab

Variables: OLLAMA_URL (por defecto http://127.0.0.1:11434), HOMELAB_DIR, NO_COLOR.
`

func main() {
	args, dir := extraerDir(os.Args[1:])
	if len(args) == 0 {
		fmt.Print(ayuda)
		return
	}
	comandos := map[string]func(dir string, args []string) error{
		"up": cmdUp, "down": cmdDown, "status": cmdStatus, "logs": cmdLogs,
		"models": cmdModels, "pull": cmdPull, "chat": cmdChat,
	}
	switch c := args[0]; {
	case c == "version" || c == "--version":
		fmt.Println("homelab", version)
	case c == "help" || c == "ayuda" || c == "-h" || c == "--help":
		fmt.Print(ayuda)
	case comandos[c] != nil:
		if err := comandos[c](dir, args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, rojo("✗ "+err.Error()))
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Comando desconocido %q. Usa: homelab help\n", c)
		os.Exit(2)
	}
}

// extraerDir saca --dir <ruta> o --dir=<ruta> de cualquier posición de los argumentos.
func extraerDir(args []string) (resto []string, dir string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "--dir="):
			dir = strings.TrimPrefix(a, "--dir=")
		default:
			resto = append(resto, a)
		}
	}
	return resto, dir
}

// parsear acepta opciones antes o después de los argumentos: `chat qwen3.5:2b --pensar`.
func parsear(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
