# Changelog

## [Unreleased]

### Added
- CLI `homelab` en Go, un binario para Linux, macOS y Windows con los comandos `up`, `down`, `status`, `logs`, `models`, `pull`, `chat`, `bench` y `version`.
- `homelab bench`: compara modelos midiendo carga en frío, RAM, velocidad de lectura y de escritura y espera hasta la primera palabra. Corre 5 verificaciones automáticas en español y guarda el informe en `resultados/`.
- Comandos dentro del chat: `/modelo`, `/modelos`, `/pensar`, `/limpiar` y `/salir`. Ctrl+C corta la respuesta sin cerrar el chat.
- Herramienta `escribir_nota`: crea notas Markdown nuevas en `archivos/notas/` y nunca sobrescribe.
- Servicios Open WebUI (chat, voz y RAG en español) y Speaches (Whisper y voces Kokoro en español).
- `.env.example` con todas las variables, y profiles por servicio para activarlos o quitarlos.
- `scripts/build.sh` para compilación cruzada; `README.md`.
- `homelab doctor`: revisa Docker, la memoria de Docker contra los límites, la escritura en la carpeta, los puertos y los modelos que no caben.
- `homelab pull` consulta el peso del modelo en el registro (Ollama o Hugging Face) y no empieza la descarga si no cabe en la memoria de Ollama (`--forzar` lo salta); `homelab bench` lo omite con un error claro.
- Suites del benchmark en archivos JSON (`pruebas/`): nueva suite `mantenimiento` con 8 tareas de administración, y suites propias en `<carpeta>/pruebas/` sin recompilar (`--suite`).
- `homelab bench compare` cruza los informes guardados por modelo, suite, equipo y fecha; los informes guardan cuantización, parámetros, suite e hilos de cada corrida.
- `homelab bench --hilos 2,4,8` repite cada modelo con cada número de hilos.
- Herramientas de solo lectura nuevas para el agente: `servicios`, `logs_servicio` (servicio de lista cerrada), `procesos` y `leer_notas`.
- `homelab ask "pregunta"`: una sola respuesta sin chat, para scripts, cron o n8n; `--nota` la guarda en `archivos/notas/`.
- `homelab rm` borra modelos; `homelab pull` documentado con modelos de Hugging Face (`hf.co/usuario/repo:cuantización`).
- `homelab up --lab`: solo Ollama, detiene los demás servicios para medir modelos.
- CI en GitHub Actions: pruebas en Linux, macOS y Windows, validación de `compose.yml` y binarios publicados al etiquetar `v*`.

### Changed
- El modelo por defecto del chat pasa de `qwen3.5:2b-q4_K_M` a `qwen3.5:4b-q4_K_M`: en el benchmark es el único modelo pequeño que consulta siempre las herramientas (31/39 aciertos frente a 22/39).
- `compose.yml`: versiones fijas (Portainer 2.45.3, Filebrowser v2.63.23, n8n 2.46.6, Open WebUI v0.12.5, Speaches 0.9.0-rc.3), healthcheck de Ollama, `OLLAMA_CONTEXT_LENGTH`, y límites, zona horaria y usuario de Filebrowser configurables.
- Los paneles escuchan por defecto solo en `127.0.0.1`; se abren a la red con `BIND_IP=0.0.0.0`.
- Las palabras clave del agente se comparan como palabras completas y sin acentos.

### Fixed
- El agente funciona en macOS y Windows; antes leía `/proc` y `/sys`, que solo existen en Linux.
- Si una herramienta falla, Ollama está caído o el modelo no está instalado, el agente da un mensaje claro en vez de cerrarse.
- `homelab chat` envía `num_thread` igual a `OLLAMA_CPUS`; antes Ollama creaba un hilo por núcleo detectado y competían por la cuota del contenedor.
- Con Docker apagado, los comandos del stack lo dicen en vez de mostrar el error del pipe.
- Falsos positivos de las palabras clave: "programación" ya no dispara la herramienta de memoria, "Discord" la de disco ni "calorías" la de temperatura.
- El prompt de sistema menciona todas las herramientas ofrecidas al modelo (faltaba `contenedores`).

### Removed
- `agent.py`, reemplazado por `homelab chat`.
