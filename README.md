# Homelab de IA local

Un binario, `homelab`, que levanta con Docker un homelab de inteligencia artificial que funciona sin internet y sin tarjeta gráfica, conversa con los modelos desde la terminal y mide su rendimiento para compararlos entre sí y entre equipos. Funciona igual en Linux, macOS y Windows.

## Qué se instala

| Servicio | Para qué | Dirección por defecto |
|---|---|---|
| Ollama | Corre los modelos de lenguaje | `http://127.0.0.1:11434` (solo este equipo) |
| Open WebUI | Chat en el navegador, voz y preguntas sobre tus documentos | `http://localhost:3000` |
| Speaches | Whisper para oír y voces Kokoro para leer en voz alta, en español | `http://localhost:8000/docs` |
| n8n | Automatizaciones | `http://localhost:5678` |
| Filebrowser | Archivos y las notas del agente | `http://localhost:8080` |
| Portainer | Panel de Docker | `https://localhost:9443` |

Todos arrancan por defecto. Para quitar uno, bórralo de `COMPOSE_PROFILES` en `.env`.

## Instalación

1. Instala Docker (Docker Desktop en macOS y Windows; Docker Engine en Linux).
2. Copia el binario de tu sistema (`dist/homelab-<so>-<arquitectura>`) y renómbralo a `homelab` (`homelab.exe` en Windows).
3. Ejecuta:

```bash
homelab up
```

`up` crea la carpeta `~/homelab` (o usa la carpeta actual si ya tiene `compose.yml`), escribe `compose.yml` y `.env`, levanta los servicios y descarga los modelos de `OLLAMA_MODELS` y los de voz. **Solo necesita internet esta primera vez**; después todo funciona sin conexión.

Recomendado: 16 GB de RAM. Con 8 GB, deja solo `ollama,openwebui` en `COMPOSE_PROFILES` y usa modelos de 2B.

## Comandos

| Comando | Qué hace |
|---|---|
| `homelab up [--sin-modelos]` | Levanta los servicios y descarga lo que falte. Vuelve a ejecutarlo tras editar `.env`. |
| `homelab down` | Detiene los servicios (los datos se conservan). |
| `homelab status` | Estado de cada servicio, versión de Ollama, modelo en memoria y URLs. |
| `homelab logs [servicio] [-f]` | Logs de los servicios, por ejemplo `homelab logs ollama -f`. |
| `homelab models` | Modelos instalados: tamaño, parámetros, cuantización y capacidades. |
| `homelab pull <modelo>...` | Descarga modelos, por ejemplo `homelab pull qwen3.5:4b-q4_K_M`. |
| `homelab chat [modelo] [--pensar]` | Conversa con el agente local. |
| `homelab bench [modelos...]` | Compara el rendimiento de los modelos. |

Opción global `--dir <ruta>` (o la variable `HOMELAB_DIR`) para elegir la carpeta del homelab. `OLLAMA_URL` cambia la dirección de Ollama y `NO_COLOR=1` quita los colores.

## Chat

```bash
homelab chat qwen3.5:2b-q4_K_M
```

Dentro del chat:

| Comando | Qué hace |
|---|---|
| `/modelo <nombre>` | Cambia de modelo sin salir |
| `/modelos` | Lista los modelos instalados |
| `/pensar` | Activa o desactiva el razonamiento (think) |
| `/limpiar` | Olvida la conversación |
| `/salir` | Termina (también Ctrl+D) |

**Ctrl+C** corta una respuesta larga sin cerrar el chat. Tras cada respuesta verás la espera hasta la primera palabra, la velocidad de lectura y de escritura en tokens/s, los tokens generados y el tiempo total.

### Herramientas del agente (política por defecto)

Un modelo de 2B se equivoca a menudo, así que el agente **solo** tiene:

- **Lectura del equipo:** disco, memoria, temperatura, tiempo encendido y número de contenedores (`docker ps -q`, con argumentos fijos).
- **Notas Markdown:** `escribir_nota` crea un archivo `.md` nuevo en `archivos/notas/` (lo ves en Filebrowser). Nunca sobrescribe: si el nombre existe, agrega `-2`, `-3`… Tamaño máximo de 64 KB y el nombre de archivo se limpia.

No existe ninguna herramienta que borre, reinicie o envíe nada. La prueba `TestHerramientasPermitidas` falla si alguien agrega una. La temperatura solo está disponible en Linux; en macOS y Windows el agente lo dice en vez de inventar una cifra.

## Benchmark

```bash
homelab bench                         # todos los modelos instalados, 3 rondas
homelab bench qwen3.5:2b-q4_K_M gemma4:e2b-it-qat --runs 1
homelab bench --pensar                # con razonamiento activado
```

Para cada modelo: lo saca de memoria, mide la carga en frío y la RAM que ocupa, y corre 6 pruebas en español:

| Prueba | Qué verifica |
|---|---|
| `velocidad` | Solo mide (explicar Docker en un párrafo) |
| `aritmetica` | Responder **exactamente** `391` a 17 × 23 (sigue instrucciones) |
| `herramienta_si` | Pedir la herramienta `memoria` cuando le preguntan por la RAM |
| `herramienta_no` | **No** pedir herramientas en una pregunta de cultura general |
| `json` | Devolver JSON válido con las claves pedidas |
| `documento_largo` | Resumir un texto de ~1500 tokens (mide la velocidad de lectura) |

Para comparar en igualdad, todas las peticiones fijan `temperature 0`, `seed 42`, `num_ctx`, `num_thread` y `num_predict`. El benchmark **nunca ejecuta** herramientas: solo revisa cuál pidió el modelo.

Cómo leer la tabla (medianas):

- **CARGA**: segundos en subir el modelo a memoria desde el disco.
- **RAM**: memoria que ocupa cargado.
- **LECTURA**: tokens/s procesando el prompt. Es lo que más pesa al preguntar sobre documentos (RAG).
- **ESCRITURA**: tokens/s generando. Por debajo de ~5 tok/s se siente lento para conversar.
- **1ª PALABRA**: espera hasta que empieza a responder una pregunta corta.
- **ACIERTOS**: verificaciones superadas.

El informe completo, con las respuestas y los datos del equipo, se guarda en `resultados/bench-<equipo>-<fecha>.json`, para comparar dispositivos o versiones.

Opciones: `--runs N`, `--ctx N` (por defecto `OLLAMA_CTX`), `--hilos N` (por defecto `OLLAMA_CPUS`), `--pensar` y `--salida <carpeta>`.

**Consejo:** `OLLAMA_CPUS` limita los núcleos del contenedor y es lo que más influye en los tokens/s. Prueba con 2 y con 4 (o más), ejecuta `homelab up` y repite el benchmark.

## Voz y documentos (Open WebUI)

- **Hablarle**: el micrófono del chat envía tu voz a Whisper (speaches) y la convierte en texto.
- **Leer en voz alta**: el botón de altavoz de cada respuesta usa la voz española `ef_dora` (o `em_alex` con `TTS_VOICE`).
- **Documentos**: adjunta un PDF o arrastra archivos al chat y pregunta. Los embeddings son multilingües (`paraphrase-multilingual-MiniLM-L12-v2`) y corren dentro de Open WebUI, así que no desplazan al modelo de chat en Ollama.

Notas:

- La primera cuenta que crees en Open WebUI es la de administrador.
- Open WebUI guarda su configuración en su base de datos desde el primer arranque. Si cambias en `.env` algo de voz o RAG después, ajústalo en *Administración → Configuración* (o usa `RESET_CONFIG_ON_START=true` una vez).
- Los navegadores solo permiten el micrófono en `localhost` o con HTTPS. Desde otro dispositivo de tu red (`http://192.168.x.x:3000`) el micrófono queda bloqueado si no pones un proxy con HTTPS delante.
- En CPU están apagadas las tareas extra (títulos, etiquetas, sugerencias y reescritura de consultas) para que tu pregunta no haga cola detrás de ellas.

## Configuración (`.env`)

Las más usadas:

| Variable | Por defecto | Qué controla |
|---|---|---|
| `COMPOSE_PROFILES` | todos | Servicios que arrancan |
| `BIND_IP` | `127.0.0.1` | `0.0.0.0` abre los paneles a tu red local |
| `OLLAMA_CPUS` / `OLLAMA_MEM` | `2` / `6g` | Núcleos y memoria de Ollama |
| `OLLAMA_CTX` | `4096` | Contexto por defecto (más contexto = más RAM) |
| `OLLAMA_DATA` | `ollama_data` | Volumen o carpeta de los modelos (por ejemplo `./modelos`) |
| `OLLAMA_MODELS` | 3 modelos | Lo que `homelab up` descarga si falta |
| `STT_MODEL` | `Systran/faster-whisper-small` | Whisper; con 32 GB prueba `deepdml/faster-whisper-large-v3-turbo-ct2` |
| `TTS_MODEL` / `TTS_VOICE` | Kokoro / `ef_dora` | Voz para leer en voz alta |

`compose.yml` lo escribe siempre el binario. Tus cambios van en `.env` o en un `compose.override.yml`, que Docker Compose carga automáticamente.

### macOS

Docker en Mac no puede usar la GPU (Metal), así que Ollama dentro de Docker corre solo con CPU. Para que vaya mucho más rápido:

1. Instala Ollama nativo.
2. Quita `ollama` de `COMPOSE_PROFILES`.
3. Pon `OLLAMA_BASE_URL=http://host.docker.internal:11434`.

El CLI sigue usando `http://127.0.0.1:11434`.

## Conectar n8n

Dentro de Docker, `127.0.0.1` es el propio contenedor. Usa los nombres de los servicios:

- **Ollama**: credencial de Ollama con la URL `http://ollama:11434`.
- **Speaches**: credencial de OpenAI con la URL base `http://speaches:8000/v1` y cualquier API key (por ejemplo `local`) para transcribir o generar voz.

## Problemas comunes

- **Windows: "The system cannot find the file specified" al guardar notas.** El acceso controlado a carpetas de Windows Defender protege `Documents`, `Desktop` y otras carpetas personales, y bloquea a `homelab.exe`. Usa la carpeta por defecto (`~/homelab`), que no está protegida, o permite `homelab.exe` en *Seguridad de Windows → Protección contra ransomware*.
- **"Ollama no responde"**: ejecuta `homelab up` y revisa `homelab logs ollama`.
- **Un modelo no cabe en memoria**: sube `OLLAMA_MEM` o baja `OLLAMA_CTX` en `.env` y ejecuta `homelab up`.

## Seguridad

- Por defecto los paneles solo se abren desde este equipo (`BIND_IP=127.0.0.1`), y Ollama siempre.
- Portainer monta `docker.sock`, que da control total del equipo: no lo abras a redes que no controlas.
- La carpeta de modelos contiene la clave privada de Ollama (`id_ed25519`). `.gitignore` excluye `modelos/`, `archivos/`, `filebrowser/database/`, `resultados/` y `.env`.

## Desarrollo

```bash
go test ./...
scripts/build.sh            # binarios para linux/darwin (amd64, arm64) y windows/amd64 en dist/
```

Requiere Go 1.26 o superior. La única dependencia externa es `gopsutil`, para leer memoria, disco y uptime en los tres sistemas.
