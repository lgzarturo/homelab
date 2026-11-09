package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ollamaFalso levanta un servidor HTTP que imita a Ollama.
func ollamaFalso(t *testing.T, h http.HandlerFunc) *Ollama {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Ollama{URL: srv.URL, HTTP: srv.Client()}
}

func TestChatStreamingYMetricas(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		var p PeticionChat
		if r.URL.Path != "/api/chat" || json.NewDecoder(r.Body).Decode(&p) != nil || !p.Stream || p.Model != "m" {
			t.Errorf("petición inesperada: %s %+v", r.URL.Path, p)
		}
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Hola"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":", mundo"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true,"load_duration":500000000,`+
			`"prompt_eval_count":100,"prompt_eval_duration":2000000000,"eval_count":30,"eval_duration":3000000000}`)
	})
	var trozos []string
	r, err := o.Chat(context.Background(), PeticionChat{Model: "m"}, func(t string, _ bool) { trozos = append(trozos, t) })
	if err != nil {
		t.Fatal(err)
	}
	m := r.Metricas
	if r.Contenido != "Hola, mundo" || len(trozos) != 2 {
		t.Errorf("contenido %q, trozos %v", r.Contenido, trozos)
	}
	if m.Lectura != 50 || m.Escritura != 10 || m.TokensPrompt != 100 || m.TokensSalida != 30 || m.Carga != 500*time.Millisecond {
		t.Errorf("métricas = %+v; quería lectura 50, escritura 10, 100/30 tokens, carga 500ms", m)
	}
	if m.PrimeraPalabra <= 0 || m.Total < m.PrimeraPalabra {
		t.Errorf("tiempos incoherentes: primera %v, total %v", m.PrimeraPalabra, m.Total)
	}
}

func TestChatLlamadasYPensamiento(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","thinking":"veamos"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"memoria","arguments":{}}}]},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true}`)
	})
	var pensado string
	r, err := o.Chat(context.Background(), PeticionChat{Model: "m"}, func(t string, pensando bool) {
		if pensando {
			pensado += t
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Llamadas) != 1 || r.Llamadas[0].Function.Name != "memoria" {
		t.Errorf("llamadas = %+v", r.Llamadas)
	}
	if pensado != "veamos" || r.Pensamiento != "veamos" || r.Contenido != "" {
		t.Errorf("el razonamiento no debe mezclarse con el contenido: %q / %q", r.Pensamiento, r.Contenido)
	}
}

func TestChatModeloNoInstalado(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'x' not found"}`)
	})
	_, err := o.Chat(context.Background(), PeticionChat{Model: "x"}, nil)
	if !errors.Is(err, ErrModeloNoInstalado) || !strings.Contains(err.Error(), "homelab pull x") {
		t.Errorf("error = %v; quería ErrModeloNoInstalado con la pista de homelab pull", err)
	}
}

func TestChatErrorDentroDelStream(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"error":"model requires more system memory"}`)
	})
	if _, err := o.Chat(context.Background(), PeticionChat{Model: "m"}, nil); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("error = %v; quería el mensaje de Ollama", err)
	}
}

func TestChatCortadoSinDoneEsError(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"a medias"},"done":false}`)
	})
	if _, err := o.Chat(context.Background(), PeticionChat{Model: "m"}, nil); err == nil || !strings.Contains(err.Error(), "cortó") {
		t.Errorf("error = %v; un stream sin done no debe contar como respuesta válida", err)
	}
}

func TestInstalarExigeSuccess(t *testing.T) {
	cuerpo := ""
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, cuerpo) })
	nada := func(string, int64, int64) {}
	cuerpo = `{"status":"pulling manifest"}` + "\n" + `{"status":"success"}` + "\n"
	if err := o.Instalar(context.Background(), "m", nada); err != nil {
		t.Errorf("descarga completa dio error: %v", err)
	}
	cuerpo = `{"status":"pulling abc","total":100,"completed":40}` + "\n"
	if err := o.Instalar(context.Background(), "m", nada); err == nil {
		t.Error("una descarga cortada debe dar error")
	}
}

func TestOllamaCaidoDaPistaClara(t *testing.T) {
	o := &Ollama{URL: "http://127.0.0.1:1", HTTP: &http.Client{}}
	_, err := o.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "homelab up") {
		t.Errorf("error = %v; quería la pista de homelab up", err)
	}
}

func TestChatCancelado(t *testing.T) {
	o := ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"empiezo"},"done":false}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // nunca termina por su cuenta
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := o.Chat(ctx, PeticionChat{Model: "m"}, func(string, bool) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v; quería context.Canceled", err)
	}
}

func TestPorSegundo(t *testing.T) {
	if porSegundo(0, 10) != 0 || porSegundo(10, 0) != 0 || porSegundo(10, int64(time.Second)) != 10 {
		t.Error("porSegundo no maneja ceros o el caso normal")
	}
}
