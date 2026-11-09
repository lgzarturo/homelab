package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Política: solo herramientas de lectura y una para notas Markdown nuevas. Si alguien agrega una
// herramienta que borre, reinicie o envíe algo, esta prueba falla.
func TestHerramientasPermitidas(t *testing.T) {
	hs := herramientas(t.TempDir())
	for _, h := range hs {
		switch h.Tipo {
		case tipoLectura:
			if len(h.Params) != 0 {
				t.Errorf("%s: una herramienta de lectura no recibe argumentos del modelo", h.Nombre)
			}
		case tipoNota:
			if h.Nombre != "escribir_nota" {
				t.Errorf("%s: solo escribir_nota puede escribir", h.Nombre)
			}
		default:
			t.Errorf("%s: tipo %q no permitido (solo lectura o notas Markdown)", h.Nombre, h.Tipo)
		}
	}
	if defs := definiciones(hs); len(defs) != len(hs) {
		t.Errorf("al modelo se le ofrecen %d herramientas y el catálogo tiene %d", len(defs), len(hs))
	}
}

func nombres(hs []Herramienta) string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Nombre)
	}
	return strings.Join(out, ",")
}

func TestHerramientasDirectasPalabrasCompletas(t *testing.T) {
	hs := herramientas(t.TempDir())
	casos := map[string]string{
		"¿Cuánta RAM tengo?":                   "memoria",
		"¿Cómo van la memoria y el disco?":     "disco,memoria",
		"¿Qué TEMPERATURA tienes?":             "temperatura",
		"¿Cuántos grados tienes?":              "temperatura",
		"¿Cuánto llevas encendida?":            "tiempo_encendido",
		"Háblame de programación":              "", // "ram" dentro de "programación"
		"¿Usas Discord?":                       "", // "disco" dentro de "discord"
		"¿Cuántas calorías tiene una manzana?": "", // "calor" dentro de "calorías"
		"Guarda una nota: comprar leche":       "", // escribir_nota nunca se dispara sola
	}
	for pregunta, want := range casos {
		if got := nombres(herramientasDirectas(pregunta, hs)); got != want {
			t.Errorf("%q dispara [%s]; quería [%s]", pregunta, got, want)
		}
	}
}

func TestEscribirNotaCreaMarkdownSinSobrescribir(t *testing.T) {
	dir := t.TempDir()
	args := map[string]any{"titulo": "Lista de compras", "contenido": "- leche\n- pan"}
	r1, err := escribirNota(dir, args)
	if err != nil {
		t.Fatal(err)
	}
	fecha := time.Now().Format("2006-01-02")
	primera := filepath.Join(dir, fecha+"-lista-de-compras.md")
	if b, _ := os.ReadFile(primera); string(b) != "# Lista de compras\n\n- leche\n- pan\n" {
		t.Errorf("contenido = %q", b)
	}
	if r1.(map[string]string)["guardada"] != "notas/"+fecha+"-lista-de-compras.md" {
		t.Errorf("resultado = %v", r1)
	}
	if _, err := escribirNota(dir, map[string]any{"titulo": "Lista de compras", "contenido": "otra"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(primera); !strings.Contains(string(b), "leche") {
		t.Error("la segunda nota sobrescribió a la primera")
	}
	if _, err := os.Stat(filepath.Join(dir, fecha+"-lista-de-compras-2.md")); err != nil {
		t.Errorf("la segunda nota debía llamarse -2: %v", err)
	}
}

func TestEscribirNotaNoSaleDeSuCarpeta(t *testing.T) {
	dir := t.TempDir()
	if _, err := escribirNota(dir, map[string]any{"titulo": `../../etc/passwd\..\x`, "contenido": "hola"}); err != nil {
		t.Fatal(err)
	}
	entradas, _ := os.ReadDir(dir)
	if len(entradas) != 1 || !strings.HasSuffix(entradas[0].Name(), "-etc-passwd-x.md") {
		t.Errorf("archivos en la carpeta de notas: %v", entradas)
	}
	if fuera, _ := os.ReadDir(filepath.Dir(dir)); len(fuera) != 1 {
		t.Errorf("se escribió fuera de la carpeta de notas: %v", fuera)
	}
}

func TestEscribirNotaValidaArgumentos(t *testing.T) {
	dir := t.TempDir()
	for _, args := range []map[string]any{
		{"titulo": "x"},
		{"contenido": "x"},
		{"titulo": 3, "contenido": "x"},
		{"titulo": "x", "contenido": strings.Repeat("a", maxNota+1)},
		{"titulo": strings.Repeat("t", maxTitulo+1), "contenido": "x"},
	} {
		if _, err := escribirNota(dir, args); err == nil {
			t.Errorf("escribirNota(%v) debería fallar", args)
		}
	}
}

func TestSlug(t *testing.T) {
	casos := map[string]string{
		"Lista de Compras!":            "lista-de-compras",
		"Árbol del ñandú":              "arbol-del-nandu",
		"***":                          "nota",
		"CON":                          "con",
		strings.Repeat("a", 70):        strings.Repeat("a", 60),
		strings.Repeat("palabra ", 20): "palabra-palabra-palabra-palabra-palabra-palabra-palabra",
	}
	for entrada, want := range casos {
		if got := slug(entrada); got != want || len(got) > 60 {
			t.Errorf("slug(%q) = %q; quería %q", entrada, got, want)
		}
	}
}

func TestHerramientasDeLecturaDevuelvenJSON(t *testing.T) {
	for _, h := range herramientas(t.TempDir()) {
		if h.Tipo != tipoLectura {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(ejecutar(h, nil)), &v); err != nil {
			t.Errorf("%s no devolvió JSON válido: %v", h.Nombre, err)
		}
	}
	var m map[string]float64
	_ = json.Unmarshal([]byte(ejecutar(herramientas("")[1], nil)), &m)
	if m["total_gb"] <= 0 {
		t.Errorf("memoria = %v; quería un total mayor que cero", m)
	}
}

func TestEjecutarConvierteErroresEnJSON(t *testing.T) {
	h := Herramienta{Nombre: "x", Ejecutar: func(map[string]any) (any, error) { return nil, errors.New("sin permiso") }}
	if got := ejecutar(h, nil); got != `{"error":"sin permiso"}` {
		t.Errorf("ejecutar = %s", got)
	}
}
