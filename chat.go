package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	memoriaCorta     = 6 // mensajes de historial: los modelos pequeños se enredan con historiales largos
	maxNotasPorTurno = 3
)

const ayudaChat = `  /modelo <nombre>  cambia de modelo (sin nombre: muestra el actual)
  /modelos          lista los modelos instalados
  /pensar           activa o desactiva el razonamiento (think)
  /limpiar          olvida la conversación
  /salir            termina (también Ctrl+D). Ctrl+C corta una respuesta larga
`

// sistema arma el prompt de sistema a partir del catálogo, así nunca se desalinea con las herramientas ofrecidas.
func sistema(hs []Herramienta) string {
	var lectura []string
	for _, h := range hs {
		if h.Tipo == tipoLectura {
			lectura = append(lectura, fmt.Sprintf("%s (%s)", h.Nombre, strings.ToLower(h.Descripcion)))
		}
	}
	return "Eres Agent, un asistente personal. Vives dentro de una mini PC sin tarjeta gráfica y funcionas sin internet. " +
		"Respondes en español, corto y con buen humor (máximo 3 frases). " +
		"Herramientas de solo lectura del equipo: " + strings.Join(lectura, ", ") + ". " +
		"Úsalas SOLO si te preguntan por eso y responde sobre lo que te preguntaron. " +
		"Nunca inventes cifras del equipo: los números salen solo de una herramienta. " +
		"Si te piden guardar o anotar algo, usa escribir_nota. " +
		"Para cualquier otra pregunta no uses herramientas: responde con lo que sabes. " +
		"Tus respuestas van en texto plano: sin asteriscos ni markdown, y no hagas preguntas al final."
}

type sesion struct {
	o         *Ollama
	modelo    string
	pensar    bool
	hs        []Herramienta
	tools     []Tool
	sistema   string
	historial []Mensaje
}

func cmdChat(flagDir string, args []string) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	pensar := fs.Bool("pensar", false, "activa el razonamiento del modelo (think)")
	pos, err := parsear(fs, args)
	if err != nil {
		return err
	}
	dir, err := resolverDir(flagDir)
	if err != nil {
		return err
	}
	modelo := modeloPorDefecto
	if ms := lista(leerEnv(dir)["OLLAMA_MODELS"]); len(ms) > 0 {
		modelo = ms[0]
	}
	if len(pos) > 0 {
		modelo = pos[0]
	}
	o := nuevoOllama()
	if err := verificarModelo(o, modelo); err != nil {
		return err
	}
	hs := herramientas(filepath.Join(dir, "archivos", "notas"))
	s := &sesion{o: o, modelo: modelo, pensar: *pensar, hs: hs, tools: definiciones(hs), sistema: sistema(hs)}
	return s.repl(os.Stdin)
}

func (s *sesion) repl(entrada io.Reader) error {
	fmt.Println(morado("🌱 Agent local") + gris(" · "+s.modelo+" · sin internet · /ayuda para comandos"))
	fmt.Println(gris("  Herramientas: solo lectura y notas Markdown nuevas. Nada que borre, reinicie o envíe.") + "\n")

	// Ctrl+C corta la respuesta en curso; en el prompt, sale.
	var mu sync.Mutex
	var cancelar context.CancelFunc
	senales := make(chan os.Signal, 1)
	signal.Notify(senales, os.Interrupt)
	defer signal.Stop(senales)
	go func() {
		for range senales {
			mu.Lock()
			c := cancelar
			mu.Unlock()
			if c == nil {
				fmt.Println()
				os.Exit(0)
			}
			c()
		}
	}()

	lector := bufio.NewScanner(entrada)
	lector.Buffer(make([]byte, 64<<10), 1<<20)
	for {
		fmt.Print(limon("tú › "))
		if !lector.Scan() {
			fmt.Println()
			if err := lector.Err(); err != nil {
				return fmt.Errorf("no pude leer lo que escribiste: %w", err)
			}
			return nil
		}
		q := strings.TrimSpace(lector.Text())
		if q == "" {
			continue
		}
		if strings.HasPrefix(q, "/") {
			if s.comando(q) {
				return nil
			}
			continue
		}
		ctx, c := context.WithCancel(context.Background())
		mu.Lock()
		cancelar = c
		mu.Unlock()
		err := s.turno(ctx, q)
		mu.Lock()
		cancelar = nil
		mu.Unlock()
		c()
		switch {
		case errors.Is(err, context.Canceled):
			fmt.Println(gris("  (cancelado)") + "\n")
		case err != nil:
			fmt.Println(rojo("  ✗ "+err.Error()) + "\n")
		}
	}
}

func (s *sesion) turno(ctx context.Context, q string) error {
	t0 := time.Now()
	mensajes := append([]Mensaje{{Role: "system", Content: s.sistema}}, s.historial...)
	mensajes = append(mensajes, Mensaje{Role: "user", Content: q})
	texto := "pensando"
	if directas := herramientasDirectas(q, s.hs); len(directas) > 0 {
		llamadas := make([]ToolCall, len(directas))
		for i, h := range directas {
			llamadas[i] = ToolCall{Function: FuncCall{Name: h.Nombre, Arguments: map[string]any{}}}
		}
		mensajes = append(mensajes, Mensaje{Role: "assistant", ToolCalls: llamadas})
		for _, h := range directas {
			mensajes = append(mensajes, s.usar(h, nil))
		}
		texto = "leyendo lo que vio"
	}
	var r Respuesta
	var primera time.Duration
	empezo := false
	notas := 0 // un 2B en bucle podría crear notas sin parar
	for vuelta := 0; ; vuelta++ {
		var err error
		r, err = s.pedir(ctx, mensajes, texto, func() {
			if !empezo {
				primera, empezo = time.Since(t0), true
			}
		})
		if err != nil {
			return err
		}
		if len(r.Llamadas) == 0 || vuelta == 3 {
			break
		}
		mensajes = append(mensajes, Mensaje{Role: "assistant", Content: r.Contenido, ToolCalls: r.Llamadas})
		for _, c := range r.Llamadas {
			h, ok := s.buscar(c.Function.Name)
			switch {
			case !ok:
				mensajes = append(mensajes, Mensaje{Role: "tool", ToolName: c.Function.Name, Content: `{"error":"esa herramienta no existe"}`})
			case h.Tipo == tipoNota && notas >= maxNotasPorTurno:
				mensajes = append(mensajes, Mensaje{Role: "tool", ToolName: h.Nombre, Content: fmt.Sprintf(`{"error":"límite de %d notas por mensaje"}`, maxNotasPorTurno)})
			default:
				if h.Tipo == tipoNota {
					notas++
				}
				mensajes = append(mensajes, s.usar(h, c.Function.Arguments))
			}
		}
		texto = "leyendo lo que vio"
	}
	contenido := strings.TrimSpace(r.Contenido)
	if contenido == "" {
		fmt.Println(gris("  (el modelo no respondió nada)") + "\n")
		return nil
	}
	// Al historial solo van pregunta y respuesta: si quedan las llamadas, el modelo repite la última respuesta.
	s.historial = append(s.historial, Mensaje{Role: "user", Content: q}, Mensaje{Role: "assistant", Content: contenido})
	if len(s.historial) > memoriaCorta {
		s.historial = s.historial[len(s.historial)-memoriaCorta:]
	}
	m := r.Metricas
	fmt.Println(gris(fmt.Sprintf("  %.1f s 1ª palabra · %.0f tok/s lectura · %.1f tok/s escritura · %d tokens · %.1f s",
		primera.Seconds(), m.Lectura, m.Escritura, m.TokensSalida, time.Since(t0).Seconds())) + "\n")
	return nil
}

// pedir hace una vuelta al modelo: ruedita hasta la primera palabra y luego el texto según llega.
func (s *sesion) pedir(ctx context.Context, mensajes []Mensaje, texto string, alEmpezar func()) (Respuesta, error) {
	rueda := girar(texto)
	defer rueda.parar()
	escribiendo, pensando := false, false
	pet := PeticionChat{Model: s.modelo, Messages: mensajes, Tools: s.tools, Think: s.pensar, Options: map[string]any{"temperature": 0.4}}
	r, err := s.o.Chat(ctx, pet, func(t string, esPensamiento bool) {
		if esPensamiento {
			rueda.parar()
			if !pensando {
				fmt.Print(gris("  💭 "))
				pensando = true
			}
			fmt.Print(gris(t))
			return
		}
		if !escribiendo {
			if t = strings.TrimLeft(t, " \t\r\n"); t == "" {
				return
			}
			rueda.parar()
			if pensando {
				fmt.Println()
			}
			fmt.Print(morado("Agent › "))
			escribiendo = true
			alEmpezar()
		}
		fmt.Print(t)
	})
	rueda.parar()
	if escribiendo || pensando {
		fmt.Println()
	}
	return r, err
}

func (s *sesion) buscar(nombre string) (Herramienta, bool) {
	for _, h := range s.hs {
		if h.Nombre == nombre {
			return h, true
		}
	}
	return Herramienta{}, false
}

// usar ejecuta una herramienta, avisa en pantalla y devuelve el mensaje "tool" para el modelo.
func (s *sesion) usar(h Herramienta, args map[string]any) Mensaje {
	resultado := ejecutar(h, args)
	if h.Tipo == tipoNota {
		var v map[string]string
		_ = json.Unmarshal([]byte(resultado), &v)
		if v["guardada"] != "" {
			fmt.Println(gris("  ✎ nota guardada en archivos/" + v["guardada"]))
		} else {
			fmt.Println(gris("  ✎ no se guardó la nota: " + v["error"]))
		}
	} else {
		fmt.Println(gris("  ⚙ mirando " + strings.ReplaceAll(h.Nombre, "_", " ") + "…"))
	}
	return Mensaje{Role: "tool", ToolName: h.Nombre, Content: resultado}
}

func parsearComando(linea string) (cmd, arg string) {
	campos := strings.Fields(linea)
	if len(campos) == 0 {
		return "", ""
	}
	return strings.ToLower(campos[0]), strings.Join(campos[1:], " ")
}

// comando atiende los comandos /…; devuelve true para salir.
func (s *sesion) comando(linea string) bool {
	cmd, arg := parsearComando(linea)
	switch cmd {
	case "/salir", "/bye", "/exit":
		return true
	case "/ayuda", "/help", "/?":
		fmt.Print(gris(ayudaChat))
	case "/limpiar":
		s.historial = nil
		fmt.Println(gris("  Conversación olvidada."))
	case "/pensar":
		s.pensar = !s.pensar
		estado := "desactivado"
		if s.pensar {
			estado = "activado"
		}
		fmt.Println(gris("  Razonamiento " + estado + "."))
	case "/modelos":
		s.listarModelos()
	case "/modelo":
		if arg == "" {
			fmt.Println(gris("  Modelo actual: " + s.modelo))
			break
		}
		if err := verificarModelo(s.o, arg); err != nil {
			fmt.Println(rojo("  ✗ " + err.Error()))
			break
		}
		s.modelo = arg
		fmt.Println(gris("  Ahora hablas con " + arg + "."))
	default:
		fmt.Println(gris("  Comando desconocido. Usa /ayuda"))
	}
	fmt.Println()
	return false
}

func (s *sesion) listarModelos() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ms, err := s.o.Tags(ctx)
	if err != nil {
		fmt.Println(rojo("  ✗ " + err.Error()))
		return
	}
	for _, m := range ms {
		marca := "  "
		if m.Name == nombreCompleto(s.modelo) {
			marca = "› "
		}
		fmt.Println(gris(fmt.Sprintf("  %s%s · %s", marca, m.Name, gb(m.Size))))
	}
}
