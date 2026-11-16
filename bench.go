package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

//go:embed bench_documento.md
var documentoLargo string

type caso struct {
	Nombre          string
	Prompt          string
	ConHerramientas bool
	Formato         string
	Mide            string               // "lectura" o "primera_palabra": de qué caso sale esa mediana
	Verificar       func(Respuesta) bool // nil: solo mide velocidad
}

type opcionesBench struct {
	Rondas int  `json:"rondas"`
	Ctx    int  `json:"num_ctx"`
	Hilos  int  `json:"num_thread"`
	Pensar bool `json:"think"`
	Tope   int  `json:"num_predict"`
}

// opciones fija todo lo que cambia el resultado para comparar en igualdad de condiciones.
func (op opcionesBench) opciones() map[string]any {
	m := map[string]any{"temperature": 0, "seed": 42, "num_ctx": op.Ctx, "num_predict": op.Tope}
	if op.Hilos > 0 {
		m["num_thread"] = op.Hilos
	}
	return m
}

type Equipo struct {
	Nombre       string  `json:"nombre"`
	SO           string  `json:"so"`
	Arquitectura string  `json:"arquitectura"`
	CPUs         int     `json:"cpus"`
	RAMGB        float64 `json:"ram_gb"`
	DockerCPUs   int     `json:"docker_cpus"`
	DockerRAMGB  float64 `json:"docker_ram_gb"`
	Ollama       string  `json:"ollama"`
}

type Prueba struct {
	Caso            string   `json:"caso"`
	Mide            string   `json:"mide,omitempty"`
	Ronda           int      `json:"ronda"`
	OK              *bool    `json:"ok,omitempty"`
	Error           string   `json:"error,omitempty"`
	PrimeraPalabraS float64  `json:"primera_palabra_s"`
	TotalS          float64  `json:"total_s"`
	LecturaTokS     float64  `json:"lectura_tok_s"`
	EscrituraTokS   float64  `json:"escritura_tok_s"`
	TokensPrompt    int      `json:"tokens_prompt"`
	TokensSalida    int      `json:"tokens_salida"`
	Herramientas    []string `json:"herramientas,omitempty"`
	Respuesta       string   `json:"respuesta"`
}

type ResultadoModelo struct {
	Modelo          string   `json:"modelo"`
	Cuantizacion    string   `json:"cuantizacion,omitempty"`
	Parametros      string   `json:"parametros,omitempty"`
	Hilos           int      `json:"hilos"` // num_thread de esta corrida; 0 = lo decidió Ollama
	Error           string   `json:"error,omitempty"`
	CargaS          float64  `json:"carga_s"`
	RAMGB           float64  `json:"ram_gb"`
	LecturaTokS     float64  `json:"lectura_tok_s"`
	EscrituraTokS   float64  `json:"escritura_tok_s"`
	PrimeraPalabraS float64  `json:"primera_palabra_s"`
	Aciertos        int      `json:"aciertos"`
	Verificados     int      `json:"verificados"`
	Errores         int      `json:"errores"`
	Pruebas         []Prueba `json:"pruebas"`
}

type Informe struct {
	Fecha    time.Time         `json:"fecha"`
	Suite    string            `json:"suite,omitempty"`
	Equipo   Equipo            `json:"equipo"`
	Opciones opcionesBench     `json:"opciones"`
	Modelos  []ResultadoModelo `json:"modelos"`
}

func cmdBench(flagDir string, args []string) error {
	dir, err := resolverDir(flagDir)
	if err != nil {
		return err
	}
	if len(args) > 0 && (args[0] == "compare" || args[0] == "comparar") {
		return cmdBenchComparar(dir, args[1:])
	}
	env := leerEnv(dir)
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	rondas := fs.Int("runs", 3, "repeticiones de la suite por modelo")
	ctxTok := fs.Int("ctx", entero(env["OLLAMA_CTX"], 4096), "contexto en tokens (num_ctx)")
	hilos := fs.String("hilos", fmt.Sprint(entero(env["OLLAMA_CPUS"], 0)), "hilos de CPU (num_thread); 0 = los decide Ollama. Varios separados por coma (2,4,8) repiten el benchmark con cada uno")
	pensar := fs.Bool("pensar", false, "activa el razonamiento (think)")
	suite := fs.String("suite", suitePorDefecto, "suite de pruebas: "+strings.Join(suitesDisponibles(dir), ", ")+", o la ruta a un .json")
	salida := fs.String("salida", filepath.Join(dir, "resultados"), "carpeta donde se guarda el informe JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Uso: homelab bench [modelos...] [opciones]   (sin modelos: todos los instalados)")
		fmt.Fprintln(fs.Output(), "     homelab bench compare [informes.json...]  (sin archivos: todos los de resultados/)")
		fs.PrintDefaults()
	}
	modelos, err := parsear(fs, args)
	if err != nil {
		return err
	}
	casos, err := cargarSuite(dir, *suite)
	if err != nil {
		return err
	}
	var listaHilos []int
	for _, h := range lista(*hilos) {
		listaHilos = append(listaHilos, entero(h, 0))
	}
	if len(listaHilos) == 0 {
		listaHilos = []int{0}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	o := nuevoOllama()
	ms, err := o.Tags(ctx)
	if err != nil {
		return err
	}
	instalados := map[string]ModeloInstalado{}
	for _, m := range ms {
		instalados[m.Name] = m
	}
	if len(modelos) == 0 {
		for _, m := range ms {
			modelos = append(modelos, m.Name)
		}
		slices.Sort(modelos)
	}
	limite := limiteOllama(env)
	if len(modelos) == 0 {
		return errors.New("no hay modelos instalados: homelab pull " + modeloPorDefecto)
	}
	op := opcionesBench{Rondas: max(*rondas, 1), Ctx: *ctxTok, Hilos: listaHilos[0], Pensar: *pensar, Tope: 256}
	if op.Pensar {
		op.Tope = 1024 // el razonamiento consume tokens antes de la respuesta
	}
	inf := Informe{Fecha: time.Now(), Suite: strings.TrimSuffix(filepath.Base(*suite), ".json"), Equipo: equipo(ctx, o), Opciones: op}
	fmt.Println(morado("🏁 Benchmark") + gris(fmt.Sprintf(" · suite %s · %d modelos · %d rondas · ctx %d · hilos %s · think %v",
		inf.Suite, len(modelos), op.Rondas, op.Ctx, *hilos, op.Pensar)))
corridas:
	for _, m := range modelos {
		inst := instalados[nombreCompleto(m)]
		if cabe(inst.Size, limite) == "no" {
			msg := fmt.Sprintf("no cabe: pesa %s y Ollama tiene %s (sube OLLAMA_MEM en .env)", gb(inst.Size), gb(limite))
			fmt.Println("\n" + morado("▸ "+m) + "\n" + rojo("  ✗ "+msg))
			inf.Modelos = append(inf.Modelos, ResultadoModelo{Modelo: m, Error: msg})
			continue
		}
		for _, h := range listaHilos {
			op.Hilos = h
			res := benchModelo(ctx, o, m, op, casos)
			res.Cuantizacion, res.Parametros = inst.Details.QuantizationLevel, inst.Details.ParameterSize
			inf.Modelos = append(inf.Modelos, res)
			if ctx.Err() != nil {
				break corridas
			}
		}
	}
	imprimirTabla(inf.Modelos)
	ruta, err := guardarInforme(*salida, inf)
	if err != nil {
		return fmt.Errorf("no pude guardar el informe: %w", err)
	}
	fmt.Println(gris("  Informe: " + ruta))
	if ctx.Err() != nil {
		fmt.Println(gris("  (interrumpido: el informe es parcial)"))
	}
	return nil
}

func hilosTexto(h int) string {
	if h <= 0 {
		return "auto"
	}
	return fmt.Sprint(h)
}

// benchModelo corre la suite contra un modelo. Nunca ejecuta herramientas: solo revisa cuál pidió el modelo.
func benchModelo(ctx context.Context, o *Ollama, modelo string, op opcionesBench, casos []caso) ResultadoModelo {
	res := ResultadoModelo{Modelo: modelo, Hilos: op.Hilos}
	fmt.Println("\n" + morado("▸ "+modelo) + gris(" · hilos "+hilosTexto(op.Hilos)))
	_ = o.Liberar(ctx, modelo) // empezar en frío
	r := girar("cargando en memoria")
	t := time.Now()
	err := o.Cargar(ctx, modelo, op.opciones())
	r.parar()
	if err != nil {
		res.Error = err.Error()
		fmt.Println(rojo("  ✗ " + res.Error))
		return res
	}
	res.CargaS = time.Since(t).Seconds()
	if ps, err := o.PS(ctx); err == nil {
		for _, p := range ps {
			if p.Name == nombreCompleto(modelo) {
				res.RAMGB = float64(p.Size) / 1e9
			}
		}
	}
	fmt.Println(gris(fmt.Sprintf("  carga en frío %.1f s · %.1f GB en memoria", res.CargaS, res.RAMGB)))
	hs := herramientas("") // sin carpeta: el bench nunca ejecuta herramientas
	tools, sis := definiciones(hs), sistema(hs)
	for ronda := 1; ronda <= op.Rondas; ronda++ {
		for i, c := range casos {
			if ctx.Err() != nil {
				return resumir(res)
			}
			rueda := girar(fmt.Sprintf("ronda %d/%d · %s", ronda, op.Rondas, c.Nombre))
			p := correrCaso(ctx, o, modelo, op, sis, tools, c, ronda, i)
			rueda.parar()
			if ctx.Err() != nil {
				return resumir(res) // Ctrl+C: la prueba a medias no cuenta
			}
			res.Pruebas = append(res.Pruebas, p)
			fmt.Println(lineaPrueba(p, op.Rondas))
		}
	}
	return resumir(res)
}

func correrCaso(ctx context.Context, o *Ollama, modelo string, op opcionesBench, sis string, tools []Tool, c caso, ronda, i int) Prueba {
	// El marcador al inicio evita que la caché de prefijo de Ollama infle la lectura en las repeticiones.
	msgs := []Mensaje{
		{Role: "system", Content: fmt.Sprintf("(prueba %d-%d) %s", ronda, i, sis)},
		{Role: "user", Content: c.Prompt},
	}
	pet := PeticionChat{Model: modelo, Messages: msgs, Format: c.Formato, Think: op.Pensar, Options: op.opciones()}
	if c.ConHerramientas {
		pet.Tools = tools
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p := Prueba{Caso: c.Nombre, Mide: c.Mide, Ronda: ronda}
	r, err := o.Chat(cctx, pet, nil)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	m := r.Metricas
	p.PrimeraPalabraS, p.TotalS = m.PrimeraPalabra.Seconds(), m.Total.Seconds()
	p.LecturaTokS, p.EscrituraTokS = m.Lectura, m.Escritura
	p.TokensPrompt, p.TokensSalida = m.TokensPrompt, m.TokensSalida
	p.Respuesta = recortar(r.Contenido, 300)
	for _, l := range r.Llamadas {
		p.Herramientas = append(p.Herramientas, l.Function.Name)
	}
	if c.Verificar != nil {
		ok := c.Verificar(r)
		p.OK = &ok
	}
	return p
}

func lineaPrueba(p Prueba, rondas int) string {
	marca := gris("·")
	switch {
	case p.Error != "":
		return rojo(fmt.Sprintf("  ✗ ronda %d/%d · %s · %s", p.Ronda, rondas, p.Caso, recortar(p.Error, 80)))
	case p.OK != nil && *p.OK:
		marca = verde("✓")
	case p.OK != nil:
		marca = rojo("✗")
	}
	return fmt.Sprintf("  %s %s", marca, gris(fmt.Sprintf("ronda %d/%d · %-16s %5.1f s · %4.0f tok/s lectura · %4.1f tok/s escritura",
		p.Ronda, rondas, p.Caso, p.TotalS, p.LecturaTokS, p.EscrituraTokS)))
}

// resumir calcula las medianas: escritura de toda respuesta con al menos 10 tokens; lectura y primera
// palabra de los casos que la suite marca con "mide" (si ninguno lo marca, de todos los casos).
func resumir(res ResultadoModelo) ResultadoModelo {
	var lectura, escritura, primera, todasLectura, todasPrimera []float64
	for _, p := range res.Pruebas {
		if p.Error != "" {
			res.Errores++
			continue
		}
		if p.OK != nil {
			res.Verificados++
			if *p.OK {
				res.Aciertos++
			}
		}
		if p.TokensSalida >= 10 {
			escritura = append(escritura, p.EscrituraTokS)
		}
		todasLectura, todasPrimera = append(todasLectura, p.LecturaTokS), append(todasPrimera, p.PrimeraPalabraS)
		switch p.Mide {
		case "lectura":
			lectura = append(lectura, p.LecturaTokS)
		case "primera_palabra":
			primera = append(primera, p.PrimeraPalabraS)
		}
	}
	if len(lectura) == 0 {
		lectura = todasLectura
	}
	if len(primera) == 0 {
		primera = todasPrimera
	}
	res.LecturaTokS, res.EscrituraTokS, res.PrimeraPalabraS = mediana(lectura), mediana(escritura), mediana(primera)
	return res
}

func mediana(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func imprimirTabla(rs []ResultadoModelo) {
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODELO\tHILOS\tCARGA\tRAM\tLECTURA\tESCRITURA\t1ª PALABRA\tACIERTOS\tERRORES")
	for _, r := range rs {
		if r.Error != "" {
			fmt.Fprintf(tw, "%s\t-\t-\t-\t-\t-\t-\t-\t%s\n", r.Modelo, recortar(r.Error, 50))
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%.1f s\t%.1f GB\t%.0f tok/s\t%.1f tok/s\t%.1f s\t%d/%d\t%d\n",
			r.Modelo, hilosTexto(r.Hilos), r.CargaS, r.RAMGB, r.LecturaTokS, r.EscrituraTokS, r.PrimeraPalabraS, r.Aciertos, r.Verificados, r.Errores)
	}
	tw.Flush()
	fmt.Println(gris("  Medianas. LECTURA: velocidad leyendo un texto largo (pesa en RAG y en logs). ESCRITURA: velocidad generando."))
}

func equipo(ctx context.Context, o *Ollama) Equipo {
	e := Equipo{SO: runtime.GOOS, Arquitectura: runtime.GOARCH, CPUs: runtime.NumCPU()}
	e.Nombre, _ = os.Hostname()
	if v, err := mem.VirtualMemory(); err == nil {
		e.RAMGB = redondear(float64(v.Total)/1e9, 1)
	}
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.NCPU}} {{.MemTotal}}").Output(); err == nil {
		var memTotal int64
		if _, err := fmt.Sscan(string(out), &e.DockerCPUs, &memTotal); err == nil {
			e.DockerRAMGB = redondear(float64(memTotal)/1e9, 1)
		}
	}
	e.Ollama, _ = o.Version(ctx)
	return e
}

func guardarInforme(dir string, inf Informe) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ruta := filepath.Join(dir, fmt.Sprintf("bench-%s-%s.json", slug(inf.Equipo.Nombre), inf.Fecha.Format("20060102-150405")))
	b, err := json.MarshalIndent(inf, "", "  ")
	if err != nil {
		return "", err
	}
	return ruta, os.WriteFile(ruta, b, 0o644)
}
