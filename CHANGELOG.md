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

### Changed
- `compose.yml`: versiones fijas (Portainer 2.45.3, Filebrowser v2.63.23, n8n 2.46.6, Open WebUI v0.12.5, Speaches 0.9.0-rc.3), healthcheck de Ollama, `OLLAMA_CONTEXT_LENGTH`, y límites, zona horaria y usuario de Filebrowser configurables.
- Los paneles escuchan por defecto solo en `127.0.0.1`; se abren a la red con `BIND_IP=0.0.0.0`.
- Las palabras clave del agente se comparan como palabras completas y sin acentos.

### Fixed
- El agente funciona en macOS y Windows; antes leía `/proc` y `/sys`, que solo existen en Linux.
- Si una herramienta falla, Ollama está caído o el modelo no está instalado, el agente da un mensaje claro en vez de cerrarse.
- Falsos positivos de las palabras clave: "programación" ya no dispara la herramienta de memoria, "Discord" la de disco ni "calorías" la de temperatura.
- El prompt de sistema menciona todas las herramientas ofrecidas al modelo (faltaba `contenedores`).

### Removed
- `agent.py`, reemplazado por `homelab chat`.
