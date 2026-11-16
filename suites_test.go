package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resp(contenido string, llamadas ...string) Respuesta {
	r := Respuesta{Contenido: contenido}
	for _, l := range llamadas {
		r.Llamadas = append(r.Llamadas, ToolCall{Function: FuncCall{Name: l}})
	}
	return r
}

// Todas las suites del binario deben cargar: un tipo de verificación mal escrito se detecta aquí.
func TestSuitesDelBinarioCargan(t *testing.T) {
	nombres := suitesDisponibles(t.TempDir())
	if strings.Join(nombres, ",") != "basica,mantenimiento" {
		t.Fatalf("suites = %v", nombres)
	}
	for _, n := range nombres {
		casos, err := cargarSuite(t.TempDir(), n)
		if err != nil {
			t.Errorf("suite %s: %v", n, err)
		}
		vistos := map[string]bool{}
		for _, c := range casos {
			if vistos[c.Nombre] {
				t.Errorf("suite %s: caso repetido %q", n, c.Nombre)
			}
			vistos[c.Nombre] = true
		}
	}
	basica, _ := cargarSuite(t.TempDir(), "basica")
	largo := basica[len(basica)-1]
	if strings.Contains(largo.Prompt, "{{") || !strings.Contains(largo.Prompt, "familia Robles") {
		t.Error("{{documento_largo}} no se reemplazó por el documento")
	}
}

// Respuestas correctas e incorrectas de cada caso de mantenimiento: la suite debe distinguirlas.
func TestSuiteMantenimientoDistingue(t *testing.T) {
	casos, err := cargarSuite(t.TempDir(), "mantenimiento")
	if err != nil {
		t.Fatal(err)
	}
	pruebas := map[string]struct{ bien, mal Respuesta }{
		"gravedad_log":      {resp(`{"gravedad":"Crítica","causa":"disco lleno"}`), resp(`{"gravedad":"media","causa":"reintento"}`)},
		"disco_lleno":       {resp("`/var/lib/docker`\n"), resp("El más lleno es /var/lib/docker")},
		"contenedor_caido":  {resp("homelab-n8n-1."), resp("homelab-speaches-1")},
		"extraer_json":      {resp(`{"servicio":"speaches","codigo":503}`), resp(`{"servicio":"speaches","codigo":500}`)},
		"resumen_log":       {resp("Open WebUI se reinicia porque se queda sin memoria al indexar el PDF."), resp("n8n falló por falta de memoria.")},
		"cron":              {resp("30 3 * * *"), resp("0 3 * * *")},
		"herramienta_disco": {resp("", "disco"), resp("Quedan 200 GB libres.")},
		"herramienta_no":    {resp("Muestra los registros de los servicios."), resp("", "contenedores")},
	}
	if len(casos) != len(pruebas) {
		t.Fatalf("la suite tiene %d casos y la prueba cubre %d", len(casos), len(pruebas))
	}
	for _, c := range casos {
		p, ok := pruebas[c.Nombre]
		if !ok {
			t.Errorf("caso sin prueba: %s", c.Nombre)
			continue
		}
		if !c.Verificar(p.bien) {
			t.Errorf("%s: rechazó una respuesta correcta: %+v", c.Nombre, p.bien)
		}
		if c.Verificar(p.mal) {
			t.Errorf("%s: aceptó una respuesta incorrecta: %+v", c.Nombre, p.mal)
		}
	}
}

func TestSuitePropiaSinRecompilar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pruebas"), 0o755); err != nil {
		t.Fatal(err)
	}
	escribir := func(nombre, contenido string) {
		if err := os.WriteFile(filepath.Join(dir, "pruebas", nombre), []byte(contenido), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	escribir("mia.json", `{"casos":[{"nombre":"saludo","prompt":"Di hola","verificar":{"tipo":"contiene","valores":["hola"]}}]}`)
	casos, err := cargarSuite(dir, "mia")
	if err != nil || len(casos) != 1 || !casos[0].Verificar(resp("¡Hola!")) {
		t.Fatalf("suite propia: %v, %+v", err, casos)
	}
	if got := suitesDisponibles(dir); strings.Join(got, ",") != "basica,mantenimiento,mia" {
		t.Errorf("suitesDisponibles = %v", got)
	}
	// Una suite propia con el mismo nombre gana a la del binario.
	escribir("basica.json", `{"casos":[{"nombre":"unico","prompt":"x"}]}`)
	if casos, _ := cargarSuite(dir, "basica"); len(casos) != 1 || casos[0].Nombre != "unico" {
		t.Errorf("la suite propia no reemplazó a la del binario: %+v", casos)
	}
	for nombre, contenido := range map[string]string{
		"tipo.json":     `{"casos":[{"nombre":"a","prompt":"x","verificar":{"tipo":"adivina"}}]}`,
		"vacia.json":    `{"casos":[]}`,
		"rota.json":     `{casos`,
		"sinvalor.json": `{"casos":[{"nombre":"a","prompt":"x","verificar":{"tipo":"exacto"}}]}`,
	} {
		escribir(nombre, contenido)
		if _, err := cargarSuite(dir, strings.TrimSuffix(nombre, ".json")); err == nil {
			t.Errorf("%s debía dar error", nombre)
		}
	}
	if _, err := cargarSuite(dir, "no-existe"); err == nil || !strings.Contains(err.Error(), "mantenimiento") {
		t.Errorf("error = %v; debía listar las suites disponibles", err)
	}
}

func TestFilasComparar(t *testing.T) {
	t1 := time.Date(2026, 11, 9, 0, 55, 0, 0, time.UTC)
	t2 := t1.Add(7 * 24 * time.Hour)
	antiguo := Informe{Fecha: t1, Equipo: Equipo{Nombre: "PC"}, Opciones: opcionesBench{Hilos: 2},
		Modelos: []ResultadoModelo{{Modelo: "qwen:2b"}, {Modelo: "roto", Error: "no cabe"}}}
	nuevo := Informe{Fecha: t2, Suite: "mantenimiento", Equipo: Equipo{Nombre: "PC"}, Opciones: opcionesBench{Hilos: 4, Pensar: true},
		Modelos: []ResultadoModelo{{Modelo: "qwen:2b", Hilos: 4}, {Modelo: "qwen:2b", Hilos: 0}, {Modelo: "gemma:e2b", Hilos: 4}}}
	filas := filasComparar([]Informe{nuevo, antiguo})
	if len(filas) != 4 {
		t.Fatalf("filas = %d; quería 4 (la corrida con error no cuenta)", len(filas))
	}
	if filas[0].R.Modelo != "gemma:e2b" || filas[1].Suite != "basica" || filas[1].R.Hilos != 2 {
		t.Errorf("orden o informe antiguo mal leído: %+v", filas[:2])
	}
	if filas[2].R.Hilos != 4 || filas[3].R.Hilos != 0 || !filas[3].Think {
		t.Errorf("en informes nuevos los hilos son los de cada corrida (0 = auto): %+v", filas[2:])
	}
}
