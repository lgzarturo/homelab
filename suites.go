package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Las suites del benchmark son archivos JSON: las del binario están en pruebas/ y cada homelab
// puede agregar las suyas en <carpeta>/pruebas/<nombre>.json sin recompilar.
//
//go:embed pruebas/*.json
var suitesFS embed.FS

const suitePorDefecto = "basica"

type verificacion struct {
	Tipo    string            `json:"tipo"`    // exacto, contiene, llama, sin_llamadas, json, no_vacia
	Valor   string            `json:"valor"`   // exacto: respuesta esperada · llama: herramienta esperada
	Valores []string          `json:"valores"` // contiene: todos deben aparecer
	Alguno  []string          `json:"alguno"`  // contiene: al menos uno debe aparecer
	Claves  []string          `json:"claves"`  // json: claves obligatorias
	Campos  map[string]string `json:"campos"`  // json: clave -> valor esperado
}

type casoJSON struct {
	Nombre       string        `json:"nombre"`
	Prompt       string        `json:"prompt"`
	Herramientas bool          `json:"herramientas"`
	Formato      string        `json:"formato"`
	Mide         string        `json:"mide"` // "lectura" o "primera_palabra": de qué caso sale esa mediana
	Verificar    *verificacion `json:"verificar"`
}

// cargarSuite lee una suite por nombre (primero <dir>/pruebas, luego las del binario) o por ruta a un .json.
func cargarSuite(dir, nombre string) ([]caso, error) {
	var datos []byte
	var err error
	switch {
	case strings.HasSuffix(nombre, ".json"):
		datos, err = os.ReadFile(nombre)
	default:
		datos, err = os.ReadFile(filepath.Join(dir, "pruebas", nombre+".json"))
		if errors.Is(err, fs.ErrNotExist) {
			datos, err = suitesFS.ReadFile("pruebas/" + nombre + ".json")
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no existe la suite %q. Disponibles: %s", nombre, strings.Join(suitesDisponibles(dir), ", "))
	}
	if err != nil {
		return nil, err
	}
	var s struct {
		Casos []casoJSON `json:"casos"`
	}
	if err := json.Unmarshal(datos, &s); err != nil {
		return nil, fmt.Errorf("la suite %q no es JSON válido: %w", nombre, err)
	}
	if len(s.Casos) == 0 {
		return nil, fmt.Errorf("la suite %q no tiene casos", nombre)
	}
	casos := make([]caso, 0, len(s.Casos))
	for _, cj := range s.Casos {
		if cj.Nombre == "" || cj.Prompt == "" {
			return nil, fmt.Errorf("suite %q: cada caso necesita nombre y prompt", nombre)
		}
		c := caso{
			Nombre:          cj.Nombre,
			Prompt:          strings.ReplaceAll(cj.Prompt, "{{documento_largo}}", documentoLargo),
			ConHerramientas: cj.Herramientas,
			Formato:         cj.Formato,
			Mide:            cj.Mide,
		}
		if cj.Verificar != nil {
			if c.Verificar, err = cj.Verificar.funcion(); err != nil {
				return nil, fmt.Errorf("suite %q, caso %q: %w", nombre, cj.Nombre, err)
			}
		}
		casos = append(casos, c)
	}
	return casos, nil
}

func suitesDisponibles(dir string) []string {
	vistas := map[string]bool{}
	propias, _ := filepath.Glob(filepath.Join(dir, "pruebas", "*.json"))
	delBinario, _ := fs.Glob(suitesFS, "pruebas/*.json")
	for _, ruta := range append(propias, delBinario...) {
		vistas[strings.TrimSuffix(filepath.Base(ruta), ".json")] = true
	}
	nombres := make([]string, 0, len(vistas))
	for n := range vistas {
		nombres = append(nombres, n)
	}
	slices.Sort(nombres)
	return nombres
}

func (v verificacion) funcion() (func(Respuesta) bool, error) {
	switch v.Tipo {
	case "exacto":
		if v.Valor == "" {
			return nil, errors.New(`"exacto" necesita "valor"`)
		}
		return exacto(v.Valor), nil
	case "contiene":
		if len(v.Valores)+len(v.Alguno) == 0 {
			return nil, errors.New(`"contiene" necesita "valores" o "alguno"`)
		}
		return contiene(v.Valores, v.Alguno), nil
	case "llama":
		if v.Valor == "" {
			return nil, errors.New(`"llama" necesita "valor" con el nombre de la herramienta`)
		}
		return llamo(v.Valor), nil
	case "sin_llamadas":
		return sinLlamadas, nil
	case "json":
		return jsonCon(v.Claves, v.Campos), nil
	case "no_vacia":
		return noVacia, nil
	}
	return nil, fmt.Errorf("tipo de verificación desconocido %q (usa exacto, contiene, llama, sin_llamadas, json o no_vacia)", v.Tipo)
}

// exacto exige que la respuesta sea solo el valor esperado (se ignoran espacios, punto final, comillas y acentos graves).
func exacto(esperado string) func(Respuesta) bool {
	return func(r Respuesta) bool { return strings.Trim(r.Contenido, " \t\r\n.`\"'") == esperado }
}

// contiene exige todos los textos de todos y, si hay, al menos uno de alguno (sin distinguir mayúsculas ni acentos).
func contiene(todos, alguno []string) func(Respuesta) bool {
	return func(r Respuesta) bool {
		texto := normalizar(r.Contenido)
		for _, t := range todos {
			if !strings.Contains(texto, normalizar(t)) {
				return false
			}
		}
		return len(alguno) == 0 || slices.ContainsFunc(alguno, func(a string) bool { return strings.Contains(texto, normalizar(a)) })
	}
}

func llamo(nombre string) func(Respuesta) bool {
	return func(r Respuesta) bool {
		return slices.ContainsFunc(r.Llamadas, func(c ToolCall) bool { return c.Function.Name == nombre })
	}
}

func sinLlamadas(r Respuesta) bool { return len(r.Llamadas) == 0 && noVacia(r) }

func noVacia(r Respuesta) bool { return strings.TrimSpace(r.Contenido) != "" }

// jsonCon exige un objeto JSON con las claves dadas y con los campos en su valor esperado
// (comparados como texto, sin mayúsculas ni acentos: 503 y "503" valen igual).
func jsonCon(claves []string, campos map[string]string) func(Respuesta) bool {
	return func(r Respuesta) bool {
		var v map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(r.Contenido)), &v) != nil {
			return false
		}
		for _, c := range claves {
			if _, ok := v[c]; !ok {
				return false
			}
		}
		for c, esperado := range campos {
			valor, ok := v[c]
			if !ok || normalizar(strings.TrimSpace(fmt.Sprint(valor))) != normalizar(esperado) {
				return false
			}
		}
		return true
	}
}
