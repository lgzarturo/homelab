package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"
)

// Política de herramientas: un modelo de 2B se equivoca a menudo, así que el agente solo tiene
// herramientas de lectura y una que crea notas Markdown nuevas. No existen herramientas que
// borren, reinicien o envíen nada; TestHerramientasPermitidas falla si alguien agrega una.
const (
	tipoLectura = "lectura"
	tipoNota    = "nota"
	maxNota     = 64 << 10
	maxTitulo   = 200
)

type Herramienta struct {
	Nombre      string
	Descripcion string
	Tipo        string          // tipoLectura o tipoNota
	Claves      []string        // palabras que la disparan sin esperar a que el modelo la pida (solo lectura)
	Params      map[string]Prop // argumentos (todos obligatorios)
	Ejecutar    func(args map[string]any) (any, error)
}

// herramientas devuelve el catálogo del agente; las notas se guardan en dirNotas.
func herramientas(dirNotas string) []Herramienta {
	return []Herramienta{
		{Nombre: "disco", Descripcion: "Espacio del disco en GB", Tipo: tipoLectura,
			Claves: []string{"disco", "espacio", "almacenamiento"}, Ejecutar: sinArgs(disco)},
		{Nombre: "memoria", Descripcion: "Memoria RAM en GB", Tipo: tipoLectura,
			Claves: []string{"memoria", "ram"}, Ejecutar: sinArgs(memoria)},
		{Nombre: "temperatura", Descripcion: "Temperatura del procesador en °C", Tipo: tipoLectura,
			Claves: []string{"temperatura", "caliente", "calor", "grados"}, Ejecutar: sinArgs(temperatura)},
		{Nombre: "tiempo_encendido", Descripcion: "Horas que lleva encendido el equipo desde que arrancó", Tipo: tipoLectura,
			Claves: []string{"encendido", "encendida", "prendido", "prendida", "uptime"}, Ejecutar: sinArgs(tiempoEncendido)},
		{Nombre: "contenedores", Descripcion: "Cuántos contenedores de Docker están corriendo", Tipo: tipoLectura,
			Claves: []string{"contenedor", "contenedores", "docker"}, Ejecutar: sinArgs(contenedores)},
		{Nombre: "escribir_nota", Descripcion: "Guarda una nota nueva en Markdown. Solo crea archivos nuevos: nunca borra ni sobrescribe.", Tipo: tipoNota,
			Params: map[string]Prop{
				"titulo":    {Type: "string", Description: "Título corto de la nota"},
				"contenido": {Type: "string", Description: "Texto de la nota en Markdown"},
			},
			Ejecutar: func(a map[string]any) (any, error) { return escribirNota(dirNotas, a) }},
	}
}

// definiciones convierte el catálogo al formato de herramientas de Ollama.
func definiciones(hs []Herramienta) []Tool {
	tools := make([]Tool, 0, len(hs))
	for _, h := range hs {
		p := Params{Type: "object", Properties: map[string]Prop{}}
		for nombre, prop := range h.Params {
			p.Properties[nombre] = prop
			p.Required = append(p.Required, nombre)
		}
		sort.Strings(p.Required)
		tools = append(tools, Tool{Type: "function", Function: ToolFunc{Name: h.Nombre, Description: h.Descripcion, Parameters: p}})
	}
	return tools
}

func sinArgs(f func() (any, error)) func(map[string]any) (any, error) {
	return func(map[string]any) (any, error) { return f() }
}

// ejecutar corre una herramienta y devuelve su resultado en JSON; un fallo vuelve como {"error": ...}, nunca como pánico.
func ejecutar(h Herramienta, args map[string]any) string {
	v, err := h.Ejecutar(args)
	if err != nil {
		v = map[string]string{"error": err.Error()}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"resultado ilegible"}`
	}
	return string(b)
}

func redondear(x float64, decimales int) float64 {
	p := math.Pow(10, float64(decimales))
	return math.Round(x*p) / p
}

func disco() (any, error) {
	raiz := "/"
	if runtime.GOOS == "windows" {
		raiz = os.Getenv("SystemDrive") + `\`
		if raiz == `\` {
			raiz = `C:\`
		}
	}
	u, err := disk.Usage(raiz)
	if err != nil {
		return nil, err
	}
	return map[string]float64{
		"total_gb": math.Round(float64(u.Total) / 1e9),
		"usado_gb": math.Round(float64(u.Used) / 1e9),
		"libre_gb": math.Round(float64(u.Free) / 1e9),
	}, nil
}

func memoria() (any, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	return map[string]float64{
		"total_gb":      redondear(float64(v.Total)/1e9, 1),
		"disponible_gb": redondear(float64(v.Available)/1e9, 1),
	}, nil
}

func temperatura() (any, error) {
	temps, _ := sensors.SensorsTemperatures() // en Mac y Windows suele no haber lecturas
	maxima := 0.0
	for _, t := range temps {
		if t.Temperature > maxima && t.Temperature < 150 {
			maxima = t.Temperature
		}
	}
	if maxima == 0 {
		return map[string]any{"cpu_c": nil, "nota": "este sistema no permite medir la temperatura: dilo así, sin suponer cómo está el procesador"}, nil
	}
	return map[string]any{"cpu_c": math.Round(maxima)}, nil
}

func tiempoEncendido() (any, error) {
	s, err := host.Uptime()
	if err != nil {
		return nil, err
	}
	return map[string]float64{"horas": redondear(float64(s)/3600, 1)}, nil
}

func contenedores() (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Solo lectura: los argumentos son fijos y nunca vienen del modelo.
	salida, err := exec.CommandContext(ctx, "docker", "ps", "-q").Output()
	if err != nil {
		return nil, fmt.Errorf("no pude consultar Docker: %v", err)
	}
	return map[string]int{"corriendo": len(strings.Fields(string(salida)))}, nil
}

var sinAcentos = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

func normalizar(s string) string { return sinAcentos.Replace(strings.ToLower(s)) }

// palabras separa un texto en palabras normalizadas (minúsculas, sin acentos).
func palabras(s string) []string {
	return strings.FieldsFunc(normalizar(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// herramientasDirectas devuelve las herramientas de lectura cuyas palabras clave aparecen completas
// en la pregunta: el modelo de 2B a veces se salta la herramienta e inventa la cifra.
func herramientasDirectas(pregunta string, hs []Herramienta) []Herramienta {
	vistas := map[string]bool{}
	for _, p := range palabras(pregunta) {
		vistas[p] = true
	}
	var out []Herramienta
	for _, h := range hs {
		if h.Tipo != tipoLectura {
			continue
		}
		for _, c := range h.Claves {
			if vistas[c] {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// escribirNota crea <dir>/AAAA-MM-DD-<titulo>.md. Nunca sobrescribe: si el nombre existe agrega -2, -3…
func escribirNota(dir string, args map[string]any) (any, error) {
	titulo, _ := args["titulo"].(string)
	contenido, _ := args["contenido"].(string)
	titulo = strings.Join(strings.Fields(titulo), " ")
	contenido = strings.TrimSpace(contenido)
	if titulo == "" || contenido == "" {
		return nil, errors.New("la nota necesita titulo y contenido")
	}
	if len(contenido) > maxNota {
		return nil, fmt.Errorf("la nota supera %d KB", maxNota>>10)
	}
	if utf8.RuneCountInString(titulo) > maxTitulo {
		return nil, fmt.Errorf("el título supera %d caracteres", maxTitulo)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := time.Now().Format("2006-01-02") + "-" + slug(titulo)
	for i := 1; i <= 99; i++ {
		nombre := base + ".md"
		if i > 1 {
			nombre = fmt.Sprintf("%s-%d.md", base, i)
		}
		f, err := os.OpenFile(filepath.Join(dir, nombre), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // O_EXCL: nunca pisa un archivo
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		_, err = fmt.Fprintf(f, "# %s\n\n%s\n", titulo, contenido)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		return map[string]string{"guardada": "notas/" + nombre}, nil
	}
	return nil, errors.New("ya hay demasiadas notas con ese título hoy")
}

// slug convierte un texto en un nombre de archivo seguro: solo [a-z0-9-], máximo 60 caracteres.
func slug(s string) string {
	var b strings.Builder
	for _, p := range palabras(s) {
		limpia := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, p)
		if limpia == "" {
			continue
		}
		if b.Len() == 0 && len(limpia) > 60 {
			limpia = limpia[:60]
		}
		sep := ""
		if b.Len() > 0 {
			sep = "-"
		}
		if b.Len()+len(sep)+len(limpia) > 60 {
			break
		}
		b.WriteString(sep + limpia)
	}
	if b.Len() == 0 {
		return "nota"
	}
	return b.String()
}
