package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestParsearComando(t *testing.T) {
	casos := map[string][2]string{
		"/modelo  gemma4:e2b-it-qat": {"/modelo", "gemma4:e2b-it-qat"},
		"/PENSAR":                    {"/pensar", ""},
		"   ":                        {"", ""},
	}
	for linea, want := range casos {
		if cmd, arg := parsearComando(linea); cmd != want[0] || arg != want[1] {
			t.Errorf("parsearComando(%q) = %q, %q; quería %q, %q", linea, cmd, arg, want[0], want[1])
		}
	}
}

func TestSistemaMencionaCadaHerramienta(t *testing.T) {
	hs := herramientas("")
	s := sistema(hs)
	for _, h := range hs {
		if !strings.Contains(s, h.Nombre) {
			t.Errorf("el prompt de sistema no menciona %s", h.Nombre)
		}
	}
}

// ollamaGuionado responde cada petición de chat con la siguiente línea del guion y guarda lo que recibió.
func ollamaGuionado(t *testing.T, guion ...string) (*Ollama, *[]PeticionChat) {
	t.Helper()
	var mu sync.Mutex
	var recibidas []PeticionChat
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		var p PeticionChat
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		recibidas = append(recibidas, p)
		n := len(recibidas)
		mu.Unlock()
		if n > len(guion) {
			t.Errorf("petición de chat de más (%d)", n)
			return
		}
		fmt.Fprintln(w, guion[n-1])
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true,"eval_count":5,"eval_duration":1000000000}`)
	})
	return o, &recibidas
}

func nuevaSesion(o *Ollama, dirNotas string) *sesion {
	hs := herramientas(dirNotas)
	return &sesion{o: o, modelo: "m", hs: hs, tools: definiciones(hs), sistema: sistema(hs)}
}

func TestTurnoAtajoDeLecturaYHistorialLimpio(t *testing.T) {
	o, recibidas := ollamaGuionado(t, `{"message":{"role":"assistant","content":"Tienes RAM de sobra."},"done":false}`)
	s := nuevaSesion(o, t.TempDir())
	if err := s.turno(context.Background(), "¿Cuánta RAM tengo?"); err != nil {
		t.Fatal(err)
	}
	msgs := (*recibidas)[0].Messages
	ultimo := msgs[len(msgs)-1]
	if ultimo.Role != "tool" || ultimo.ToolName != "memoria" || !strings.Contains(ultimo.Content, "total_gb") {
		t.Errorf("el modelo debía recibir la lectura de memoria; último mensaje: %+v", ultimo)
	}
	if len(s.historial) != 2 || s.historial[0].Role != "user" || s.historial[1].Content != "Tienes RAM de sobra." {
		t.Errorf("historial = %+v; quería solo pregunta y respuesta", s.historial)
	}
}

func TestTurnoNotaPedidaPorElModelo(t *testing.T) {
	dir := t.TempDir()
	o, recibidas := ollamaGuionado(t,
		`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"escribir_nota","arguments":{"titulo":"Ideas","contenido":"probar gemma"}}}]},"done":false}`,
		`{"message":{"role":"assistant","content":"Listo, anotado."},"done":false}`)
	s := nuevaSesion(o, dir)
	if err := s.turno(context.Background(), "Anota que quiero probar gemma"); err != nil {
		t.Fatal(err)
	}
	entradas, _ := os.ReadDir(dir)
	if len(entradas) != 1 || !strings.HasSuffix(entradas[0].Name(), "-ideas.md") {
		t.Errorf("notas creadas: %v", entradas)
	}
	segunda := (*recibidas)[1].Messages
	if r := segunda[len(segunda)-1]; r.Role != "tool" || !strings.Contains(r.Content, "guardada") {
		t.Errorf("el modelo debía recibir el resultado de la nota: %+v", r)
	}
}

func TestTurnoHerramientaInexistenteNoEjecutaNada(t *testing.T) {
	dir := t.TempDir()
	o, recibidas := ollamaGuionado(t,
		`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"borrar_todo","arguments":{}}}]},"done":false}`,
		`{"message":{"role":"assistant","content":"No puedo hacer eso."},"done":false}`)
	s := nuevaSesion(o, dir)
	if err := s.turno(context.Background(), "Borra todo"); err != nil {
		t.Fatal(err)
	}
	segunda := (*recibidas)[1].Messages
	if r := segunda[len(segunda)-1]; !strings.Contains(r.Content, "no existe") {
		t.Errorf("el modelo debía saber que la herramienta no existe: %+v", r)
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != 0 {
		t.Errorf("no debía tocarse nada: %v", entradas)
	}
}

func TestTurnoLimitaNotasPorMensaje(t *testing.T) {
	dir := t.TempDir()
	var llamadas []string
	for i := 0; i < 5; i++ {
		llamadas = append(llamadas, fmt.Sprintf(`{"function":{"name":"escribir_nota","arguments":{"titulo":"nota %d","contenido":"x"}}}`, i))
	}
	o, recibidas := ollamaGuionado(t,
		`{"message":{"role":"assistant","content":"","tool_calls":[`+strings.Join(llamadas, ",")+`]},"done":false}`,
		`{"message":{"role":"assistant","content":"Hecho."},"done":false}`)
	s := nuevaSesion(o, dir)
	if err := s.turno(context.Background(), "Haz muchas notas"); err != nil {
		t.Fatal(err)
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != maxNotasPorTurno {
		t.Errorf("se crearon %d notas; el máximo por mensaje es %d", len(entradas), maxNotasPorTurno)
	}
	segunda := (*recibidas)[1].Messages
	if r := segunda[len(segunda)-1]; !strings.Contains(r.Content, "límite") {
		t.Errorf("el modelo debía saber que llegó al límite: %+v", r)
	}
}

func TestHistorialSeRecorta(t *testing.T) {
	guion := make([]string, 5)
	for i := range guion {
		guion[i] = fmt.Sprintf(`{"message":{"role":"assistant","content":"respuesta %d"},"done":false}`, i)
	}
	o, _ := ollamaGuionado(t, guion...)
	s := nuevaSesion(o, t.TempDir())
	for i := range guion {
		if err := s.turno(context.Background(), fmt.Sprintf("hola %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.historial) != memoriaCorta || s.historial[len(s.historial)-1].Content != "respuesta 4" {
		t.Errorf("historial de %d mensajes; quería los últimos %d", len(s.historial), memoriaCorta)
	}
}
