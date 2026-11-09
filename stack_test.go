package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func mismaCarpeta(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

func TestResolverDirPrioridad(t *testing.T) {
	tmp := t.TempDir()
	conCompose := filepath.Join(tmp, "con-compose")
	sinCompose := filepath.Join(tmp, "sin-compose")
	ajeno := filepath.Join(tmp, "otra-app")
	home := filepath.Join(tmp, "home")
	for _, d := range []string{conCompose, sinCompose, ajeno, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(conCompose, "compose.yml"), []byte("name: homelab\nservices: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ajeno, "compose.yml"), []byte("services:\n  web: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Run("--dir gana a todo", func(t *testing.T) {
		t.Setenv("HOMELAB_DIR", sinCompose)
		t.Chdir(conCompose)
		got, _ := resolverDir(conCompose + "/../sin-compose")
		if !mismaCarpeta(t, got, sinCompose) {
			t.Errorf("resolverDir(--dir) = %s; quería %s", got, sinCompose)
		}
	})
	t.Run("HOMELAB_DIR gana a la carpeta actual", func(t *testing.T) {
		t.Setenv("HOMELAB_DIR", sinCompose)
		t.Chdir(conCompose)
		got, _ := resolverDir("")
		if !mismaCarpeta(t, got, sinCompose) {
			t.Errorf("resolverDir() = %s; quería %s", got, sinCompose)
		}
	})
	t.Run("carpeta actual si tiene compose.yml", func(t *testing.T) {
		t.Setenv("HOMELAB_DIR", "")
		t.Chdir(conCompose)
		got, _ := resolverDir("")
		if !mismaCarpeta(t, got, conCompose) {
			t.Errorf("resolverDir() = %s; quería %s", got, conCompose)
		}
	})
	t.Run("si no, ~/homelab", func(t *testing.T) {
		t.Setenv("HOMELAB_DIR", "")
		t.Chdir(sinCompose)
		got, _ := resolverDir("")
		if want := filepath.Join(home, "homelab"); got != want {
			t.Errorf("resolverDir() = %s; quería %s", got, want)
		}
	})
	t.Run("ignora el compose.yml de otra app", func(t *testing.T) {
		t.Setenv("HOMELAB_DIR", "")
		t.Chdir(ajeno)
		got, _ := resolverDir("")
		if want := filepath.Join(home, "homelab"); got != want {
			t.Errorf("resolverDir() = %s; quería %s", got, want)
		}
	})
}

func TestPrepararDirNoPisaComposeAjeno(t *testing.T) {
	dir := t.TempDir()
	suyo := []byte("services:\n  web: {}\n")
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), suyo, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepararDir(dir); err == nil || !strings.Contains(err.Error(), "otro proyecto") {
		t.Fatalf("prepararDir debía negarse; error = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(b) != string(suyo) {
		t.Error("se modificó el compose.yml de otra app")
	}
	if _, err := os.Stat(filepath.Join(dir, "archivos")); err == nil {
		t.Error("no debía crear carpetas en un proyecto ajeno")
	}
	if _, err := dirExistente(dir); err == nil {
		t.Error("down/logs/status no deben operar sobre un proyecto ajeno")
	}
}

func TestPrepararDirCarpetaNueva(t *testing.T) {
	dir := t.TempDir()
	actualizado, err := prepararDir(dir)
	if err != nil || !actualizado {
		t.Fatalf("prepararDir = %v, %v; quería true, nil", actualizado, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(b) != string(composeYML) {
		t.Error("compose.yml no coincide con el embebido")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != string(envEjemplo) {
		t.Error(".env no coincide con .env.example")
	}
	for _, ruta := range []string{"archivos/notas", "filebrowser/database", "filebrowser/config/settings.json"} {
		if _, err := os.Stat(filepath.Join(dir, ruta)); err != nil {
			t.Errorf("falta %s: %v", ruta, err)
		}
	}
	if actualizado, _ := prepararDir(dir); actualizado {
		t.Error("la segunda vez compose.yml no debería marcarse como actualizado")
	}
}

func TestPrepararDirNoPisaEnvYRestauraCompose(t *testing.T) {
	dir := t.TempDir()
	mio := []byte("BIND_IP=0.0.0.0\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), mio, 0o644); err != nil {
		t.Fatal(err)
	}
	viejo := []byte("name: homelab\nservices: {} # versión anterior\n")
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), viejo, 0o644); err != nil {
		t.Fatal(err)
	}
	actualizado, err := prepararDir(dir)
	if err != nil || !actualizado {
		t.Fatalf("prepararDir = %v, %v", actualizado, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != string(mio) {
		t.Errorf(".env fue sobrescrito: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(b) != string(composeYML) {
		t.Error("compose.yml viejo no fue reemplazado por el del binario")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "compose.yml.bak")); string(b) != string(viejo) {
		t.Errorf("compose.yml.bak = %q; quería la versión anterior", b)
	}
}

func TestPrepararDirReutilizaModelosExistentes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modelos", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepararDir(dir); err != nil {
		t.Fatal(err)
	}
	if env := leerEnv(dir); env["OLLAMA_DATA"] != "./modelos" {
		t.Errorf("OLLAMA_DATA = %q; quería ./modelos", env["OLLAMA_DATA"])
	}
}

func TestLeerEnv(t *testing.T) {
	dir := t.TempDir()
	contenido := "# comentario\r\nA=1\r\n\r\nB = \"dos\"\nC='tres'\nD=x=y\nsin_igual\nE=a,b # comentario\nF=a#b\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(contenido), 0o644); err != nil {
		t.Fatal(err)
	}
	env := leerEnv(dir)
	want := map[string]string{"A": "1", "B": "dos", "C": "tres", "D": "x=y", "E": "a,b", "F": "a#b"}
	if len(env) != len(want) {
		t.Errorf("leerEnv = %v; quería %v", env, want)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q; quería %q", k, env[k], v)
		}
	}
	if len(leerEnv(t.TempDir())) != 0 {
		t.Error("sin .env debería devolver un mapa vacío")
	}
}

func TestEnteroListaYPerfiles(t *testing.T) {
	for entrada, want := range map[string]int{"2": 2, "1.5": 1, " 4 ": 4, "": 7, "x": 7} {
		if got := entero(entrada, 7); got != want {
			t.Errorf("entero(%q) = %d; quería %d", entrada, got, want)
		}
	}
	if got := lista(" a, b,,c "); strings.Join(got, "|") != "a|b|c" {
		t.Errorf("lista = %v", got)
	}
	env := map[string]string{"COMPOSE_PROFILES": "ollama, n8n"}
	if !perfilActivo(env, "n8n") || perfilActivo(env, "portainer") {
		t.Error("perfilActivo no respeta COMPOSE_PROFILES")
	}
}

// Contrato entre compose.yml y .env.example: toda variable usada está documentada y todo servicio arranca por defecto.
func TestEnvEjemploCubreCompose(t *testing.T) {
	env := map[string]string{}
	for _, linea := range strings.Split(string(envEjemplo), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(linea), "="); ok && !strings.HasPrefix(k, "#") {
			env[k] = v
		}
	}
	for _, m := range regexp.MustCompile(`\$\{([A-Z_]+)`).FindAllStringSubmatch(string(composeYML), -1) {
		if _, ok := env[m[1]]; !ok {
			t.Errorf("compose.yml usa ${%s} pero .env.example no la define", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`profiles: \[(\w+)\]`).FindAllStringSubmatch(string(composeYML), -1) {
		if !perfilActivo(env, m[1]) {
			t.Errorf("el servicio con profile %q no arranca por defecto (falta en COMPOSE_PROFILES)", m[1])
		}
	}
	if !marcaHomelab.Match(composeYML) {
		t.Error("compose.yml debe llevar `name: homelab` para reconocer la carpeta propia")
	}
	if env["BIND_IP"] != "127.0.0.1" {
		t.Errorf("BIND_IP por defecto = %q; los paneles deben quedar solo en localhost", env["BIND_IP"])
	}
}

func TestExtraerDirYParsear(t *testing.T) {
	resto, dir := extraerDir([]string{"chat", "--dir", "/tmp/x", "qwen", "--dir=/otra"})
	if dir != "/otra" || strings.Join(resto, " ") != "chat qwen" {
		t.Errorf("extraerDir = %v, %q", resto, dir)
	}
}
