package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"
)

// filaComparar es una corrida de un modelo dentro de un informe guardado.
type filaComparar struct {
	Fecha  time.Time
	Equipo string
	Suite  string
	Think  bool
	R      ResultadoModelo
}

// filasComparar aplana los informes en una fila por modelo y corrida, ordenadas por modelo, equipo y fecha.
// Los informes anteriores a las suites y al barrido de hilos se leen como suite "basica" con los hilos del informe.
func filasComparar(infs []Informe) []filaComparar {
	var filas []filaComparar
	for _, inf := range infs {
		for _, r := range inf.Modelos {
			if r.Error != "" {
				continue
			}
			suite := inf.Suite
			if suite == "" { // informe antiguo
				suite, r.Hilos = suitePorDefecto, inf.Opciones.Hilos
			}
			filas = append(filas, filaComparar{Fecha: inf.Fecha, Equipo: inf.Equipo.Nombre, Suite: suite, Think: inf.Opciones.Pensar, R: r})
		}
	}
	slices.SortStableFunc(filas, func(a, b filaComparar) int {
		return cmp.Or(cmp.Compare(a.R.Modelo, b.R.Modelo), cmp.Compare(a.Suite, b.Suite),
			cmp.Compare(a.Equipo, b.Equipo), a.Fecha.Compare(b.Fecha))
	})
	return filas
}

// cmdBenchComparar cruza los informes guardados: sin argumentos, todos los de <dir>/resultados.
func cmdBenchComparar(dir string, archivos []string) error {
	if len(archivos) == 0 {
		archivos, _ = filepath.Glob(filepath.Join(dir, "resultados", "bench-*.json"))
	}
	if len(archivos) == 0 {
		return fmt.Errorf("no hay informes en %s: corre `homelab bench` primero", filepath.Join(dir, "resultados"))
	}
	var infs []Informe
	for _, a := range archivos {
		b, err := os.ReadFile(a)
		if err != nil {
			return err
		}
		var inf Informe
		if err := json.Unmarshal(b, &inf); err != nil {
			return fmt.Errorf("%s no es un informe válido: %w", a, err)
		}
		infs = append(infs, inf)
	}
	filas := filasComparar(infs)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODELO\tCUANT.\tSUITE\tEQUIPO\tFECHA\tHILOS\tTHINK\tRAM\tLECTURA\tESCRITURA\t1ª PALABRA\tACIERTOS")
	for _, f := range filas {
		r := f.R
		think := "no"
		if f.Think {
			think = "sí"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.1f GB\t%.0f tok/s\t%.1f tok/s\t%.1f s\t%d/%d\n",
			r.Modelo, cmp.Or(r.Cuantizacion, "-"), f.Suite, f.Equipo, f.Fecha.Format("2006-01-02 15:04"),
			hilosTexto(r.Hilos), think, r.RAMGB, r.LecturaTokS, r.EscrituraTokS, r.PrimeraPalabraS, r.Aciertos, r.Verificados)
	}
	tw.Flush()
	fmt.Println(gris(fmt.Sprintf("  %d corridas de %d informes.", len(filas), len(infs))))
	return nil
}
