// Motor de mentira para desarrollar HU-05 sin el motor de Go.
//
//   npm run mock        (en una terminal)
//   npm run dev:mock    (en otra: Vite con el proxy apuntando a este mock)
//
// Imita lo que el front necesita del motor:
//   - GET /health      -> estado del servicio
//   - GET /posiciones  -> ultima foto de posiciones
//   - WS  /ws          -> mensaje "posiciones" cada 2 s, con un adelantamiento
//                         al azar
//
// No valida la sesion: eso solo lo hace el motor real.

import { createServer } from "node:http";
import { WebSocketServer } from "ws";

const PUERTO = Number(process.env.MOCK_PORT ?? 8090);
const INTERVALO_MS = Number(process.env.MOCK_INTERVALO_MS ?? 2000);
const CARRERA_ID = "2026_costa_azul";

// Parrilla inicial (pilotos ficticios de los mockups). `ritmo` son los
// segundos de diferencia con el lider.
const pilotos = [
  { numero: 4, codigo: "VAR", nombre: "L. Varga", equipo: "Scuderia Roja", color_equipo: "#E10600", ritmo: 0 },
  { numero: 16, codigo: "OKA", nombre: "M. Okafor", equipo: "Plata Racing", color_equipo: "#C0C4CC", ritmo: 2.4 },
  { numero: 55, codigo: "SAN", nombre: "T. Santoro", equipo: "Ambar GP", color_equipo: "#F59E0B", ritmo: 5.1 },
  { numero: 11, codigo: "RIO", nombre: "J. Ríos", equipo: "Azul Celeste", color_equipo: "#38BDF8", ritmo: 5.9 },
  { numero: 63, codigo: "LIN", nombre: "K. Lindqvist", equipo: "Plata Racing", color_equipo: "#C0C4CC", ritmo: 9.7 },
  { numero: 23, codigo: "NAV", nombre: "S. Navarro", equipo: "Azul Celeste", color_equipo: "#38BDF8", ritmo: 12.3 },
  { numero: 27, codigo: "BRA", nombre: "E. Brandt", equipo: "Scuderia Roja", color_equipo: "#E10600", ritmo: 14.0 },
  { numero: 10, codigo: "DUV", nombre: "A. Duval", equipo: "Ambar GP", color_equipo: "#F59E0B", ritmo: 21.6 },
  { numero: 31, codigo: "MOR", nombre: "D. Moreau", equipo: "Verde Norte", color_equipo: "#22C55E", ritmo: 24.2 },
  { numero: 77, codigo: "KAI", nombre: "R. Kaito", equipo: "Verde Norte", color_equipo: "#22C55E", ritmo: 30.8 },
];

let vuelta = 18;
let ticks = 0;
let actualizadoEn = new Date().toISOString();

// Arma la foto de posiciones con el formato del contrato (src/api/tipos.ts).
function foto() {
  return {
    carrera_id: CARRERA_ID,
    vuelta,
    actualizado_en: actualizadoEn,
    emitido_en: new Date().toISOString(),
    pilotos: pilotos.map((p, i) => ({
      posicion: i + 1,
      numero: p.numero,
      codigo: p.codigo,
      nombre: p.nombre,
      equipo: p.equipo,
      color_equipo: p.color_equipo,
      diferencia: i === 0 ? "Líder" : `+${(p.ritmo - pilotos[0].ritmo).toFixed(1)}`,
    })),
  };
}

// Simula un adelantamiento: intercambia dos pilotos vecinos al azar (y sus
// diferencias, para que sigan en orden). Cada 15 ticks avanza una vuelta.
function avanzar() {
  const i = Math.floor(Math.random() * (pilotos.length - 1));
  [pilotos[i], pilotos[i + 1]] = [pilotos[i + 1], pilotos[i]];
  [pilotos[i].ritmo, pilotos[i + 1].ritmo] = [pilotos[i + 1].ritmo, pilotos[i].ritmo];
  ticks += 1;
  if (ticks % 15 === 0) vuelta += 1;
  actualizadoEn = new Date().toISOString();
}

// ---- REST ----
const servidor = createServer((req, res) => {
  // Responde JSON con el codigo indicado.
  const responder = (status, cuerpo) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(cuerpo));
  };
  const ruta = new URL(req.url ?? "/", "http://localhost").pathname;

  if (req.method === "GET" && ruta === "/health") return responder(200, { service: "motor", status: "ok", mock: true });
  if (req.method === "GET" && ruta === "/posiciones") return responder(200, foto());
  return responder(404, { error: "no existe en el mock" });
});

// ---- WebSocket ----
const wss = new WebSocketServer({ server: servidor, path: "/ws" });

// Envia el sobre { type, payload } a todos los clientes conectados.
function difundir(type, payload) {
  const datos = JSON.stringify({ type, payload });
  for (const cliente of wss.clients) {
    if (cliente.readyState === cliente.OPEN) cliente.send(datos);
  }
}

wss.on("connection", () => {
  console.log(`[mock] cliente conectado (total=${wss.clients.size})`);
});

// Cada intervalo cambia las posiciones y las difunde.
setInterval(() => {
  avanzar();
  difundir("posiciones", foto());
}, INTERVALO_MS);

servidor.listen(PUERTO, () => {
  console.log(`[mock] motor falso en http://localhost:${PUERTO} (GET /posiciones, WS /ws)`);
  console.log(`[mock] emite "posiciones" cada ${INTERVALO_MS} ms. Ctrl+C para detenerlo.`);
});
