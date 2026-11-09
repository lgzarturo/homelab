package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

const modeloPorDefecto = "qwen3.5:2b-q4_K_M"

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

func cmdPull(_ string, args []string) error {
	if len(args) == 0 {
		return errors.New("uso: homelab pull <modelo>... (ej. homelab pull " + modeloPorDefecto + ")")
	}
	o := nuevoOllama()
	for _, m := range args {
		if err := instalar(o, m); err != nil {
			return err
		}
	}
	return nil
}

// instalarModelos descarga los modelos de la lista que todavía no estén instalados.
func instalarModelos(o *Ollama, modelos []string) error {
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
		if err := instalar(o, m); err != nil {
			return err
		}
	}
	return nil
}

func instalar(o *Ollama, modelo string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
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
