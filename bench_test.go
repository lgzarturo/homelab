package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestVerificadores(t *testing.T) {
	r := func(contenido string, llamadas ...string) Respuesta {
		res := Respuesta{Contenido: contenido}
		for _, l := range llamadas {
			res.Llamadas = append(res.Llamadas, ToolCall{Function: FuncCall{Name: l}})
		}
		return res
	}
	num := soloNumero("391")
	casos := []struct {
		nombre string
		ok     bool
	}{
		{"391 exacto", num(r("391"))},
		{"391 con punto", num(r(" 391.\n"))},
		{"391 con texto no vale", !num(r("Son 391"))},
		{"llamó a memoria", llamo("memoria")(r("", "memoria"))},
		{"no llamó a memoria", !llamo("memoria")(r("", "disco"))},
		{"sin llamadas y con texto", sinLlamadas(r("Un contenedor es…"))},
		{"con llamada no vale", !sinLlamadas(r("x", "contenedores"))},
		{"json válido", jsonConClaves("nombre", "edad")(r(`{"nombre":"Ana","edad":30}`))},
		{"json sin clave", !jsonConClaves("nombre", "edad")(r(`{"nombre":"Ana"}`))},
		{"json roto", !jsonConClaves("nombre")(r(`nombre: Ana`))},
	}
	for _, c := range casos {
		if !c.ok {
			t.Error(c.nombre)
		}
	}
}

func TestMediana(t *testing.T) {
	entrada := []float64{4, 1, 3, 2}
	if mediana(nil) != 0 || mediana([]float64{3, 1, 2}) != 2 || mediana(entrada) != 2.5 {
		t.Error("mediana incorrecta")
	}
	if entrada[0] != 4 {
		t.Error("mediana no debe reordenar la entrada")
	}
}

func TestResumir(t *testing.T) {
	si, no := true, false
	res := resumir(ResultadoModelo{Pruebas: []Prueba{
		{Caso: "velocidad", PrimeraPalabraS: 1, EscrituraTokS: 10, TokensSalida: 50},
		{Caso: "velocidad", PrimeraPalabraS: 3, EscrituraTokS: 20, TokensSalida: 50},
		{Caso: "aritmetica", OK: &si, EscrituraTokS: 99, TokensSalida: 2}, // muy pocos tokens: no cuenta para escritura
		{Caso: "json", OK: &no, EscrituraTokS: 30, TokensSalida: 20},
		{Caso: "documento_largo", OK: &si, LecturaTokS: 80, EscrituraTokS: 15, TokensSalida: 40},
		{Caso: "documento_largo", Error: "timeout"},
	}})
	if res.Aciertos != 2 || res.Verificados != 3 || res.Errores != 1 {
		t.Errorf("aciertos %d/%d, errores %d", res.Aciertos, res.Verificados, res.Errores)
	}
	if res.PrimeraPalabraS != 2 || res.LecturaTokS != 80 || res.EscrituraTokS != 17.5 {
		t.Errorf("medianas: primera %.1f, lectura %.1f, escritura %.1f", res.PrimeraPalabraS, res.LecturaTokS, res.EscrituraTokS)
	}
}

// Un modelo "perfecto" simulado debe acertar las 5 verificaciones con opciones fijas en cada petición.
func TestBenchModeloContraOllamaFalso(t *testing.T) {
	var mu sync.Mutex
	var chats []PeticionChat
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/generate":
			fmt.Fprint(w, `{"done":true}`)
		case "/api/ps":
			fmt.Fprint(w, `{"models":[{"name":"m:latest","size":2000000000}]}`)
		case "/api/chat":
			var p PeticionChat
			_ = json.NewDecoder(r.Body).Decode(&p)
			mu.Lock()
			chats = append(chats, p)
			mu.Unlock()
			pregunta := p.Messages[len(p.Messages)-1].Content
			mensaje := `{"role":"assistant","content":"Una respuesta de prueba en español."}`
			switch {
			case strings.Contains(pregunta, "17 × 23"):
				mensaje = `{"role":"assistant","content":"391"}`
			case strings.Contains(pregunta, "memoria RAM"):
				mensaje = `{"role":"assistant","content":"","tool_calls":[{"function":{"name":"memoria","arguments":{}}}]}`
			case p.Format == "json":
				mensaje = `{"role":"assistant","content":"{\"nombre\":\"Ana\",\"edad\":30}"}`
			}
			fmt.Fprintf(w, `{"message":%s,"done":false}`+"\n", mensaje)
			fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":200,"prompt_eval_duration":2000000000,"eval_count":20,"eval_duration":2000000000}`)
		default:
			t.Errorf("ruta inesperada %s", r.URL.Path)
		}
	})
	op := opcionesBench{Rondas: 1, Ctx: 2048, Hilos: 2, Tope: 256}
	res := benchModelo(context.Background(), o, "m", op)
	if res.Error != "" || res.Aciertos != 5 || res.Verificados != 5 || len(res.Pruebas) != len(suiteBench()) {
		t.Fatalf("resultado = %+v", res)
	}
	if res.RAMGB != 2 || res.LecturaTokS != 100 || res.EscrituraTokS != 10 {
		t.Errorf("RAM %.1f, lectura %.0f, escritura %.1f", res.RAMGB, res.LecturaTokS, res.EscrituraTokS)
	}
	for _, p := range chats {
		opc := p.Options
		if opc["temperature"] != 0.0 || opc["seed"] != 42.0 || opc["num_ctx"] != 2048.0 || opc["num_thread"] != 2.0 {
			t.Errorf("opciones no fijas: %v", opc)
		}
		if !strings.HasPrefix(p.Messages[0].Content, "(prueba ") {
			t.Errorf("falta el marcador anti-caché: %q", p.Messages[0].Content[:20])
		}
	}
}
