package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ErrModeloNoInstalado se devuelve cuando Ollama responde 404 para un modelo.
var ErrModeloNoInstalado = errors.New("modelo no instalado")

// Ollama es un cliente mínimo de la API HTTP de Ollama.
type Ollama struct {
	URL  string
	HTTP *http.Client
}

func nuevoOllama() *Ollama {
	u := os.Getenv("OLLAMA_URL")
	if u == "" {
		u = "http://127.0.0.1:11434"
	}
	return &Ollama{URL: strings.TrimRight(u, "/"), HTTP: &http.Client{}}
}

type FuncCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolCall struct {
	Function FuncCall `json:"function"`
}

type Mensaje struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type Prop struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type Params struct {
	Type       string          `json:"type"`
	Properties map[string]Prop `json:"properties"`
	Required   []string        `json:"required,omitempty"`
}

type ToolFunc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  Params `json:"parameters"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function ToolFunc `json:"function"`
}

type PeticionChat struct {
	Model    string         `json:"model"`
	Messages []Mensaje      `json:"messages"`
	Tools    []Tool         `json:"tools,omitempty"`
	Format   string         `json:"format,omitempty"`
	Think    bool           `json:"think"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

// Metricas de una respuesta. Lectura = tokens/s procesando el prompt; Escritura = tokens/s generando.
type Metricas struct {
	PrimeraPalabra time.Duration
	Total          time.Duration
	Carga          time.Duration
	TokensPrompt   int
	TokensSalida   int
	Lectura        float64
	Escritura      float64
}

type Respuesta struct {
	Contenido   string
	Pensamiento string
	Llamadas    []ToolCall
	Metricas    Metricas
}

// Chat pide una respuesta en streaming. alTrozo (opcional) recibe el texto según llega;
// pensando=true marca el razonamiento del modelo.
func (o *Ollama) Chat(ctx context.Context, p PeticionChat, alTrozo func(texto string, pensando bool)) (Respuesta, error) {
	var r Respuesta
	p.Stream = true
	cuerpo, err := json.Marshal(p)
	if err != nil {
		return r, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/chat", bytes.NewReader(cuerpo))
	if err != nil {
		return r, err
	}
	req.Header.Set("Content-Type", "application/json")
	t0 := time.Now()
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return r, o.errorConexion(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r, errorHTTP(resp, p.Model)
	}
	dec := json.NewDecoder(resp.Body)
	terminado, empezo := false, false
	for !terminado {
		var d struct {
			Message            Mensaje `json:"message"`
			Done               bool    `json:"done"`
			Error              string  `json:"error"`
			LoadDuration       int64   `json:"load_duration"`
			PromptEvalCount    int     `json:"prompt_eval_count"`
			PromptEvalDuration int64   `json:"prompt_eval_duration"`
			EvalCount          int     `json:"eval_count"`
			EvalDuration       int64   `json:"eval_duration"`
		}
		if err := dec.Decode(&d); err != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return r, errors.New("Ollama cortó la respuesta antes de terminar")
			}
			return r, fmt.Errorf("respuesta ilegible de Ollama: %w", err)
		}
		if d.Error != "" {
			return r, errors.New(d.Error)
		}
		m := d.Message
		// Bandera y no "== 0": con el reloj de baja resolución de Windows la primera palabra puede medir 0.
		if !empezo && (m.Content != "" || m.Thinking != "" || len(m.ToolCalls) > 0) {
			r.Metricas.PrimeraPalabra, empezo = time.Since(t0), true
		}
		r.Llamadas = append(r.Llamadas, m.ToolCalls...)
		if m.Thinking != "" {
			r.Pensamiento += m.Thinking
			if alTrozo != nil {
				alTrozo(m.Thinking, true)
			}
		}
		if m.Content != "" {
			r.Contenido += m.Content
			if alTrozo != nil {
				alTrozo(m.Content, false)
			}
		}
		if d.Done {
			r.Metricas.Carga = time.Duration(d.LoadDuration)
			r.Metricas.TokensPrompt, r.Metricas.TokensSalida = d.PromptEvalCount, d.EvalCount
			r.Metricas.Lectura = porSegundo(d.PromptEvalCount, d.PromptEvalDuration)
			r.Metricas.Escritura = porSegundo(d.EvalCount, d.EvalDuration)
			terminado = true
		}
	}
	r.Metricas.Total = time.Since(t0)
	return r, nil
}

func porSegundo(n int, ns int64) float64 {
	if n <= 0 || ns <= 0 {
		return 0
	}
	return float64(n) / time.Duration(ns).Seconds()
}

type ModeloInstalado struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Details struct {
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
}

type ModeloCargado struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (o *Ollama) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	err := o.pedirJSON(ctx, http.MethodGet, "/api/version", nil, &v)
	return v.Version, err
}

func (o *Ollama) Tags(ctx context.Context) ([]ModeloInstalado, error) {
	var v struct {
		Models []ModeloInstalado `json:"models"`
	}
	err := o.pedirJSON(ctx, http.MethodGet, "/api/tags", nil, &v)
	return v.Models, err
}

func (o *Ollama) PS(ctx context.Context) ([]ModeloCargado, error) {
	var v struct {
		Models []ModeloCargado `json:"models"`
	}
	err := o.pedirJSON(ctx, http.MethodGet, "/api/ps", nil, &v)
	return v.Models, err
}

func (o *Ollama) Capacidades(ctx context.Context, modelo string) ([]string, error) {
	var v struct {
		Capabilities []string `json:"capabilities"`
	}
	err := o.pedirJSON(ctx, http.MethodPost, "/api/show", map[string]any{"model": modelo}, &v)
	return v.Capabilities, err
}

// Cargar sube el modelo a memoria sin generar nada.
func (o *Ollama) Cargar(ctx context.Context, modelo string, opciones map[string]any) error {
	return o.pedirJSON(ctx, http.MethodPost, "/api/generate", map[string]any{"model": modelo, "stream": false, "options": opciones}, nil)
}

// Liberar saca el modelo de la memoria (keep_alive 0).
func (o *Ollama) Liberar(ctx context.Context, modelo string) error {
	return o.pedirJSON(ctx, http.MethodPost, "/api/generate", map[string]any{"model": modelo, "stream": false, "keep_alive": 0}, nil)
}

// Instalar descarga un modelo (api/pull) informando el progreso.
func (o *Ollama) Instalar(ctx context.Context, modelo string, progreso func(estado string, hecho, total int64)) error {
	cuerpo, _ := json.Marshal(map[string]any{"model": modelo, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/pull", bytes.NewReader(cuerpo))
	if err != nil {
		return err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return o.errorConexion(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorHTTP(resp, "")
	}
	dec := json.NewDecoder(resp.Body)
	ultimo := ""
	for {
		var d struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := dec.Decode(&d); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				if ultimo != "success" {
					return fmt.Errorf("la descarga de %s se cortó (último estado: %q)", modelo, ultimo)
				}
				return nil
			}
			return err
		}
		ultimo = d.Status
		if d.Error != "" {
			return fmt.Errorf("no pude instalar %s: %s", modelo, d.Error)
		}
		progreso(d.Status, d.Completed, d.Total)
	}
}

// pedirJSON hace una petición simple; si la entrada trae "model", un 404 se traduce a ErrModeloNoInstalado.
func (o *Ollama) pedirJSON(ctx context.Context, metodo, ruta string, entrada map[string]any, salida any) error {
	var cuerpo io.Reader
	modelo, _ := entrada["model"].(string)
	if entrada != nil {
		b, err := json.Marshal(entrada)
		if err != nil {
			return err
		}
		cuerpo = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, o.URL+ruta, cuerpo)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return o.errorConexion(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorHTTP(resp, modelo)
	}
	if salida == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(salida)
}

func errorHTTP(resp *http.Response, modelo string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	if resp.StatusCode == http.StatusNotFound && modelo != "" {
		return fmt.Errorf("%w: %s (instálalo con: homelab pull %s)", ErrModeloNoInstalado, modelo, modelo)
	}
	msg := e.Error
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	return fmt.Errorf("Ollama respondió %d: %s", resp.StatusCode, msg)
}

func (o *Ollama) errorConexion(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("Ollama no responde en %s: ¿corriste `homelab up`? (%v)", o.URL, err)
}
