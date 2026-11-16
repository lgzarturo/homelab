package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

const modeloPorDefecto = "qwen3.5:4b-q4_K_M"

// nombreCompleto agrega la etiqueta :latest que Ollama asume cuando no se indica.
func nombreCompleto(m string) string {
	if !strings.Contains(m, ":") {
		return m + ":latest"
	}
	return m
}

func verificarModelo(o *Ollama, modelo string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ms, err := o.Tags(ctx)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.Name == nombreCompleto(modelo) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s (instálalo con: homelab pull %s)", ErrModeloNoInstalado, modelo, modelo)
}

func cmdModels(_ string, _ []string) error {
	o := nuevoOllama()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ms, err := o.Tags(ctx)
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		fmt.Println("No hay modelos instalados. Prueba: homelab pull " + modeloPorDefecto)
		return nil
	}
	enMemoria := map[string]bool{}
	if ps, err := o.PS(ctx); err == nil {
		for _, p := range ps {
			enMemoria[p.Name] = true
		}
	}
	slices.SortFunc(ms, func(a, b ModeloInstalado) int { return strings.Compare(a.Name, b.Name) })
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODELO\tTAMAÑO\tPARÁMETROS\tCUANT.\tCAPACIDADES\tEN MEMORIA")
	for _, m := range ms {
		caps, _ := o.Capacidades(ctx, m.Name)
		caps = slices.DeleteFunc(caps, func(c string) bool { return c == "completion" })
		memoria := "no"
		if enMemoria[m.Name] {
			memoria = "sí"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Name, gb(m.Size), m.Details.ParameterSize, m.Details.QuantizationLevel, strings.Join(caps, ", "), memoria)
	}
	return tw.Flush()
}

func cmdPull(flagDir string, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	forzar := fs.Bool("forzar", false, "descarga aunque el modelo no quepa en la memoria de Ollama")
	modelos, err := parsear(fs, args)
	if err != nil {
		return err
	}
	if len(modelos) == 0 {
		return errors.New("uso: homelab pull <modelo>... [--forzar] (ej. homelab pull " + modeloPorDefecto + ")")
	}
	var limite int64
	if !*forzar {
		if dir, err := resolverDir(flagDir); err == nil {
			limite = limiteOllama(leerEnv(dir))
		}
	}
	o := nuevoOllama()
	for _, m := range modelos {
		if err := instalar(o, m, limite); err != nil {
			return err
		}
	}
	return nil
}

func cmdRm(_ string, args []string) error {
	if len(args) == 0 {
		return errors.New("uso: homelab rm <modelo>... (mira los nombres con: homelab models)")
	}
	o := nuevoOllama()
	for _, m := range args {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := o.Borrar(ctx, m)
		cancel()
		if errors.Is(err, ErrModeloNoInstalado) {
			return fmt.Errorf("%s no está instalado", m)
		}
		if err != nil {
			return err
		}
		fmt.Println(verde("  ✓ ") + "borrado " + m)
	}
	return nil
}

// instalarModelos descarga los modelos de la lista que todavía no estén instalados.
func instalarModelos(o *Ollama, modelos []string, limite int64) error {
	if len(modelos) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ms, err := o.Tags(ctx)
	cancel()
	if err != nil {
		return err
	}
	instalados := map[string]bool{}
	for _, m := range ms {
		instalados[m.Name] = true
	}
	for _, m := range modelos {
		if instalados[nombreCompleto(m)] {
			fmt.Println(verde("  ✓ ") + m)
			continue
		}
		if err := instalar(o, m, limite); err != nil {
			return err
		}
	}
	return nil
}

// Registros donde se consulta el peso de un modelo antes de descargarlo.
var registroOllama, registroHF = "https://registry.ollama.ai", "https://huggingface.co"

// urlManifiesto devuelve la dirección del manifiesto de un modelo: nombre[:etiqueta] de la
// biblioteca de Ollama, usuario/nombre, o hf.co/usuario/repo[:cuantización].
func urlManifiesto(modelo string) string {
	nombre, etiqueta := modelo, "latest"
	if i := strings.LastIndex(modelo, ":"); i > strings.LastIndex(modelo, "/") {
		nombre, etiqueta = modelo[:i], modelo[i+1:]
	}
	for _, prefijo := range []string{"hf.co/", "huggingface.co/"} {
		if resto, ok := strings.CutPrefix(nombre, prefijo); ok {
			return registroHF + "/v2/" + resto + "/manifests/" + etiqueta
		}
	}
	if !strings.Contains(nombre, "/") {
		nombre = "library/" + nombre
	}
	return registroOllama + "/v2/" + nombre + "/manifests/" + etiqueta
}

// pesoRemoto pregunta al registro cuánto pesa un modelo sin descargarlo; 0 si no se pudo saber.
func pesoRemoto(ctx context.Context, modelo string) int64 {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlManifiesto(modelo), nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var m struct {
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&m) != nil {
		return 0
	}
	var total int64
	for _, l := range m.Layers {
		total += l.Size
	}
	return total
}

// instalar descarga un modelo. Con limite > 0 consulta antes su peso en el registro y no empieza si
// no cabe en la memoria de Ollama (una descarga cortada a medias deja un archivo parcial del tamaño completo).
func instalar(o *Ollama, modelo string, limite int64) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if limite > 0 {
		if peso := pesoRemoto(ctx, modelo); cabe(peso, limite) == "no" {
			return fmt.Errorf("%s no cabe: pesa %s y Ollama tiene %s. Sube OLLAMA_MEM en .env, elige una versión más pequeña o usa --forzar",
				modelo, gb(peso), gb(limite))
		}
	}
	ultimo := ""
	err := o.Instalar(ctx, modelo, func(estado string, hecho, total int64) {
		linea := fmt.Sprintf("  ⬇ %s · %s", modelo, estado)
		if total > 0 {
			linea += fmt.Sprintf(" %3.0f%% de %s", 100*float64(hecho)/float64(total), gb(total))
		}
		if color {
			fmt.Print("\r\033[K" + gris(linea))
		} else if estado != ultimo {
			fmt.Println(linea)
		}
		ultimo = estado
	})
	if color {
		fmt.Print("\r\033[K")
	}
	if err != nil {
		return err
	}
	fmt.Println(verde("  ✓ ") + modelo)
	return nil
}
