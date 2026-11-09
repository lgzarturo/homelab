#!/usr/bin/env python3
"""Agent local: un agente que vive en la mini PC, sin internet ni tarjeta gráfica.
Responde en español y puede mirar cómo está el equipo con 4 herramientas de solo lectura
(disco, memoria, temperatura, tiempo encendido). Nunca muestra IPs, usuarios ni procesos.
Uso:  python3 agent.py [modelo]      (por defecto qwen3.5:2b-q4_K_M)"""
import json, os, shutil, sys, threading, time, urllib.request
import subprocess

MODELO = sys.argv[1] if len(sys.argv) > 1 else "qwen3.5:2b-q4_K_M"
API = os.environ.get("OLLAMA_URL", "http://127.0.0.1:11434")
MORADO, LIMON, GRIS, FIN = "\033[38;2;139;92;246m", "\033[38;2;253;224;71m", "\033[38;2;156;148;176m", "\033[0m"

def disco():
    t, u, l = shutil.disk_usage("/")
    return {"total_gb": round(t / 1e9), "usado_gb": round(u / 1e9), "libre_gb": round(l / 1e9)}

def memoria():
    m = dict(l.split(":", 1) for l in open("/proc/meminfo"))
    kb = lambda k: int(m[k].split()[0])
    return {"total_gb": round(kb("MemTotal") / 1e6, 1), "disponible_gb": round(kb("MemAvailable") / 1e6, 1)}

def temperatura():
    zonas = [z for z in os.listdir("/sys/class/thermal") if z.startswith("thermal_zone")]
    temps = [int(open(f"/sys/class/thermal/{z}/temp").read()) / 1000 for z in zonas]
    return {"cpu_c": round(max(temps)) if temps else None}

def tiempo_encendido():
    s = float(open("/proc/uptime").read().split()[0])
    return {"horas": round(s / 3600, 1)}

def contenedores():
    salida = subprocess.run(["docker", "ps", "-q"], capture_output=True, text=True).stdout
    return {"corriendo": len(salida.split())}

HERRAMIENTAS = {f.__name__: f for f in (disco, memoria, temperatura, tiempo_encendido, contenedores)}
DESC = {
  "disco": "Espacio del disco en GB",
  "memoria": "Memoria RAM en GB",
  "temperatura": "Temperatura del procesador en °C",
  "tiempo_encendido": "Cuánto tiempo lleva encendida la mini PC (horas desde que arrancó)",
  "contenedores": "Cuántos contenedores de Docker están corriendo"
}

# Palabras que disparan una herramienta sin esperar a que el modelo la pida: el 2B a veces se la salta e inventa la cifra.
CLAVES = {
  "disco": ("disco", "espacio", "almacenamiento"),
  "memoria": ("memoria", "ram"),
  "temperatura": ("temperatura", "caliente", "calor", "grados"),
  "tiempo_encendido": ("encendid", "prendid", "uptime"),
  "contenedores": ("contenedores", "contenedor", "docker")
}

TOOLS = [{"type": "function", "function": {"name": n, "description": d, "parameters": {"type": "object", "properties": {}}}} for n, d in DESC.items()]

SISTEMA = ("Eres Agent, un asistente Personal. Vives dentro de una mini PC sin tarjeta gráfica y funcionas sin internet. "
           "Respondes en español, corto y con buen humor (máximo 3 frases). Usa una herramienta SOLO si te preguntan por el disco, la memoria, la temperatura o cuánto llevas encendido, y responde sobre lo que te preguntaron. Nunca inventes cifras de la mini PC: los números salen solo de una herramienta. Para cualquier otra pregunta no uses herramientas: responde con lo que sabes. Puedes conversar, explicar cosas de tecnología y revisar el estado de la mini PC. Texto plano: sin asteriscos ni markdown, y no hagas preguntas al final.")

class Ruedita:
    """Muestra «⠋ pensando… 12 s» mientras el modelo no ha escrito nada."""
    def __init__(self, texto):
        self.texto, self.fin, self.t0 = texto, threading.Event(), time.time()
        self.hilo = threading.Thread(target=self._girar, daemon=True); self.hilo.start()
    def _girar(self):
        i = 0
        while not self.fin.wait(0.1):
            print(f"\r{GRIS}  {'⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'[i % 10]} {self.texto}… {time.time() - self.t0:.0f} s{FIN}", end="", flush=True); i += 1
    def parar(self):
        if not self.fin.is_set(): self.fin.set(); self.hilo.join(); print("\r\033[K", end="", flush=True)

def chat(mensajes, texto="pensando"):
    """Pide una respuesta en streaming: ruedita hasta la primera palabra y luego el texto según llega."""
    body = {"model": MODELO, "messages": mensajes, "stream": True, "think": False, "options": {"temperature": 0.4}, "tools": TOOLS}
    req = urllib.request.Request(f"{API}/api/chat", json.dumps(body).encode(), {"Content-Type": "application/json"})
    rueda, contenido, calls, final = Ruedita(texto), "", [], {}
    try:
        for linea in urllib.request.urlopen(req, timeout=600):
            d = json.loads(linea); m = d.get("message", {})
            calls += m.get("tool_calls") or []
            trozo = m.get("content", "")
            if trozo and not contenido.strip(): trozo = trozo.lstrip()
            if trozo:
                if not contenido: rueda.parar(); print(f"{MORADO}Agent ›{FIN} ", end="")
                print(trozo, end="", flush=True); contenido += trozo
            if d.get("done"): final = d
    finally:
        rueda.parar()
    if contenido: print()
    return contenido, calls, final

def main():
    print(f"{MORADO}🌱 Agent local{FIN} {GRIS}· {MODELO} · sin internet · sin tarjeta gráfica{FIN}\n")
    mensajes = [{"role": "system", "content": SISTEMA}]
    while True:
        try: q = input(f"{LIMON}tú ›{FIN} ").strip()
        except (EOFError, KeyboardInterrupt): print(); break
        if not q: continue
        mensajes = [mensajes[0]] + mensajes[1:][-6:]  # memoria corta: los modelos pequeños se enredan con historiales largos
        mensajes.append({"role": "user", "content": q})
        inicio = len(mensajes)
        t0, texto = time.time(), "pensando"
        directas = [n for n, claves in CLAVES.items() if any(k in q.lower() for k in claves)]
        if directas:
            mensajes.append({"role": "assistant", "content": "", "tool_calls": [{"function": {"name": n, "arguments": {}}} for n in directas]})
            for n in directas:
                print(f"{GRIS}  ⚙ mirando {n.replace('_', ' ')}…{FIN}")
                mensajes.append({"role": "tool", "tool_name": n, "content": json.dumps(HERRAMIENTAS[n]())})
            texto = "leyendo lo que vio"
        for vuelta in range(4):
            contenido, calls, final = chat(mensajes, texto)
            if not calls or vuelta == 3: break
            mensajes.append({"role": "assistant", "content": contenido, "tool_calls": calls})
            for c in calls:
                n = c["function"]["name"]
                print(f"{GRIS}  ⚙ mirando {n.replace('_', ' ')}…{FIN}")
                mensajes.append({"role": "tool", "tool_name": n, "content": json.dumps(HERRAMIENTAS[n]() if n in HERRAMIENTAS else {"error": "no existe"})})
            texto = "leyendo lo que vio"
        del mensajes[inicio:]  # fuera las llamadas a herramientas: si se quedan, el modelo repite la última respuesta
        mensajes.append({"role": "assistant", "content": contenido})
        tps = final.get("eval_count", 0) / max(final.get("eval_duration", 1) / 1e9, 1e-9)
        print(f"{GRIS}  {time.time() - t0:.1f} s · {tps:.1f} tokens/s{FIN}\n")

if __name__ == "__main__":
    main()