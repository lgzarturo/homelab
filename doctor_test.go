package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestBytesDe(t *testing.T) {
	casos := map[string]int64{
		"6g": 6 << 30, "6G": 6 << 30, "3gb": 3 << 30, "512m": 512 << 20, "1.5g": 3 << 29,
		"64k": 64 << 10, "1024": 1024, "": 0, "mucho": 0, "-2g": 0,
	}
	for entrada, want := range casos {
		if got := bytesDe(entrada); got != want {
			t.Errorf("bytesDe(%q) = %d; quería %d", entrada, got, want)
		}
	}
}

func TestCabe(t *testing.T) {
	const g = 1 << 30
	casos := []struct {
		peso, limite int64
		want         string
	}{
		{2 * g, 6 * g, "sí"},
		{5 * g, 6 * g, "sí"},
		{11 * g / 2, 6 * g, "justo"}, // 5.5 GB: menos de 15 % de margen
		{6*g + 1, 6 * g, "no"},
		{12 * g, 6 * g, "no"},
		{12 * g, 0, "sí"}, // Ollama nativo: sin límite conocido
		{0, 6 * g, "sí"},  // peso desconocido
	}
	for _, c := range casos {
		if got := cabe(c.peso, c.limite); got != c.want {
			t.Errorf("cabe(%d, %d) = %q; quería %q", c.peso, c.limite, got, c.want)
		}
	}
}

func TestPresupuesto(t *testing.T) {
	todos := map[string]string{"COMPOSE_PROFILES": "ollama,openwebui,speaches,n8n,filebrowser,portainer"}
	if got, want := presupuesto(todos), int64(6<<30+2<<30+3<<30+600<<20); got != want {
		t.Errorf("presupuesto por defecto = %d; quería %d", got, want)
	}
	lab := map[string]string{"COMPOSE_PROFILES": "ollama", "OLLAMA_MEM": "14g", "SPEACHES_MEM": "9g"}
	if got, want := presupuesto(lab), int64(14<<30); got != want {
		t.Errorf("presupuesto solo Ollama = %d; quería %d (los servicios apagados no cuentan)", got, want)
	}
}

func TestPuertosOcupados(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ocupado := l.Addr().(*net.TCPAddr).Port
	original := puertos
	defer func() { puertos = original }()
	puertos = []struct {
		perfil    string
		puerto    int
		soloLocal bool
	}{{"ollama", ocupado, true}, {"n8n", ocupado, false}}
	if got := puertosOcupados(map[string]string{"COMPOSE_PROFILES": "ollama"}); len(got) != 1 || got[0] != ocupado {
		t.Errorf("puertosOcupados = %v; quería solo [%d] (n8n está apagado)", got, ocupado)
	}
}

// registroFalso sirve manifiestos con el peso indicado y cuenta cuántas descargas llegan a Ollama.
func registroFalso(t *testing.T, peso int64) (o *Ollama, descargas *int) {
	t.Helper()
	descargas = new(int)
	o = ollamaFalso(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/35b"):
			fmt.Fprintf(w, `{"layers":[{"size":%d},{"size":1000}]}`, peso-1000)
		case r.URL.Path == "/api/pull":
			*descargas++
			fmt.Fprintln(w, `{"status":"success"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	original := registroOllama
	registroOllama = o.URL
	t.Cleanup(func() { registroOllama = original })
	return o, descargas
}

func TestInstalarNoEmpiezaSiNoCabe(t *testing.T) {
	o, descargas := registroFalso(t, 12_000_000_000)
	err := instalar(o, "grande:35b", 6<<30)
	if err == nil || !strings.Contains(err.Error(), "no cabe") || !strings.Contains(err.Error(), "12.0 GB") {
		t.Errorf("error = %v; quería el aviso de que no cabe con el peso", err)
	}
	if *descargas != 0 {
		t.Error("no debía empezar la descarga: deja un archivo parcial del tamaño completo")
	}
}

func TestInstalarDescargaSiCabeOSinLimite(t *testing.T) {
	o, descargas := registroFalso(t, 12_000_000_000)
	if err := instalar(o, "grande:35b", 0); err != nil { // --forzar u Ollama nativo
		t.Errorf("sin límite debía descargar: %v", err)
	}
	if err := instalar(o, "grande:35b", 14<<30); err != nil {
		t.Errorf("cabe en 14 GB: %v", err)
	}
	if err := instalar(o, "sin-manifiesto:1b", 6<<30); err != nil { // peso desconocido: no bloquea
		t.Errorf("sin manifiesto debía descargar: %v", err)
	}
	if *descargas != 3 {
		t.Errorf("descargas = %d; quería 3", *descargas)
	}
}

func TestURLManifiesto(t *testing.T) {
	casos := map[string]string{
		"qwen3.5:2b-q4_K_M": registroOllama + "/v2/library/qwen3.5/manifests/2b-q4_K_M",
		"gemma4":            registroOllama + "/v2/library/gemma4/manifests/latest",
		"usuario/modelo:7b": registroOllama + "/v2/usuario/modelo/manifests/7b",
		"hf.co/unsloth/Qwen3.6-35B-A3B-GGUF:UD-IQ2_M": registroHF + "/v2/unsloth/Qwen3.6-35B-A3B-GGUF/manifests/UD-IQ2_M",
		"huggingface.co/a/b":                          registroHF + "/v2/a/b/manifests/latest",
	}
	for modelo, want := range casos {
		if got := urlManifiesto(modelo); got != want {
			t.Errorf("urlManifiesto(%q) = %s; quería %s", modelo, got, want)
		}
	}
}

func TestChatEnviaHilos(t *testing.T) {
	o, recibidas := ollamaGuionado(t,
		`{"message":{"role":"assistant","content":"Hola."},"done":false}`,
		`{"message":{"role":"assistant","content":"Hola."},"done":false}`)
	s := nuevaSesion(o, t.TempDir())
	s.hilos = 2
	if err := s.turno(context.Background(), "hola"); err != nil {
		t.Fatal(err)
	}
	if got := (*recibidas)[0].Options["num_thread"]; got != 2.0 {
		t.Errorf("num_thread = %v; quería 2 (OLLAMA_CPUS)", got)
	}
	s.hilos = 0
	if err := s.turno(context.Background(), "hola otra vez"); err != nil {
		t.Fatal(err)
	}
	if _, hay := (*recibidas)[1].Options["num_thread"]; hay {
		t.Error("con hilos 0 no debe enviarse num_thread: lo decide Ollama")
	}
}
