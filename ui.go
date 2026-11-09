package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// color indica si la salida es una terminal que entiende colores ANSI (NO_COLOR los apaga).
var color = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func pintar(codigo, s string) string {
	if !color {
		return s
	}
	return codigo + s + "\033[0m"
}

func morado(s string) string { return pintar("\033[38;2;139;92;246m", s) }
func limon(s string) string  { return pintar("\033[38;2;253;224;71m", s) }
func gris(s string) string   { return pintar("\033[38;2;156;148;176m", s) }
func verde(s string) string  { return pintar("\033[38;2;74;222;128m", s) }
func rojo(s string) string   { return pintar("\033[38;2;248;113;113m", s) }

// ruedita muestra «⠋ pensando… 12 s» mientras no hay nada que enseñar. Sin terminal no dibuja nada.
type ruedita struct {
	fin, hecho chan struct{}
	una        sync.Once
}

func girar(texto string) *ruedita {
	r := &ruedita{fin: make(chan struct{}), hecho: make(chan struct{})}
	go func() {
		defer close(r.hecho)
		if !color {
			<-r.fin
			return
		}
		marcos, t0 := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"), time.Now()
		tic := time.NewTicker(100 * time.Millisecond)
		defer tic.Stop()
		for i := 0; ; i++ {
			select {
			case <-r.fin:
				fmt.Print("\r\033[K")
				return
			case <-tic.C:
				fmt.Print("\r" + gris(fmt.Sprintf("  %c %s… %.0f s", marcos[i%len(marcos)], texto, time.Since(t0).Seconds())))
			}
		}
	}()
	return r
}

func (r *ruedita) parar() { r.una.Do(func() { close(r.fin); <-r.hecho }) }

// gb formatea bytes como gigabytes con un decimal.
func gb(b int64) string { return fmt.Sprintf("%.1f GB", float64(b)/1e9) }

// recortar acorta un texto a n caracteres (no bytes).
func recortar(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
