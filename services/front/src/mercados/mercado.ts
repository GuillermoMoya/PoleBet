// Logica de HU-11 sin React, para poder probarla sola.
//
// El ciclo del mercado lo decide el reloj del SERVIDOR (RNF-05). El front
// solo lo muestra: cada mensaje trae `ahora` (la hora del motor) y con ella se
// calcula el desfase del reloj del navegador, asi la cuenta regresiva coincide
// con la del servidor aunque el computador tenga la hora corrida.

import type { Mercado, PeticionMercado } from "../api/tipos";

// Limites que tambien valida el motor (internal/mercados/servicio.go).
export const MAX_LARGO_PREGUNTA = 200;
export const MAX_LARGO_OPCION = 100;
export const MIN_OPCIONES = 2;
export const MAX_OPCIONES = 10;

// Lo que ve el jugador:
//   abierto     se puede apostar
//   observacion cerro la ventana de apuestas; se observa la carrera 1 minuto
//   liquidando  termino la observacion; se espera el resultado (HU-07)
//   liquidado   ya hay resultado
export type FaseMercado = "abierto" | "observacion" | "liquidando" | "liquidado";

export const TEXTO_FASE: Record<FaseMercado, string> = {
  abierto: "Abierto",
  observacion: "Cerrado · en observación",
  liquidando: "Cerrado · liquidando",
  liquidado: "Liquidado",
};

const ESTADOS = ["abierto", "cerrado", "liquidado"];

// Revisa que lo que llego por la red tenga la forma de un mercado.
export function esMercado(x: unknown): x is Mercado {
  if (typeof x !== "object" || x === null) return false;
  const m = x as Record<string, unknown>;
  return (
    typeof m.id === "string" &&
    typeof m.pregunta === "string" &&
    Array.isArray(m.opciones) &&
    m.opciones.every((o) => typeof o === "string") &&
    typeof m.estado === "string" &&
    ESTADOS.includes(m.estado) &&
    typeof m.cierra_en === "string" &&
    typeof m.observa_hasta === "string"
  );
}

// Milisegundos que hay que sumarle al reloj local para tener la hora del
// servidor. 0 si el mensaje no trae una hora valida.
export function desfaseDe(m: Pick<Mercado, "ahora">, ahoraLocal = Date.now()): number {
  const servidor = Date.parse(m.ahora);
  return Number.isNaN(servidor) ? 0 : servidor - ahoraLocal;
}

export function faseDe(m: Mercado, ahoraServidor: number): FaseMercado {
  if (m.estado === "liquidado") return "liquidado";
  if (m.estado === "abierto" && ahoraServidor < Date.parse(m.cierra_en)) return "abierto";
  // Cerrado, o "abierto" pero su ventana ya paso segun el servidor (el
  // mensaje mercado_cerrado puede tardar un instante en llegar).
  return ahoraServidor < Date.parse(m.observa_hasta) ? "observacion" : "liquidando";
}

// Segundos enteros que faltan para `iso` (redondeados hacia arriba), minimo 0.
export function segundosHasta(iso: string, ahoraServidor: number): number {
  const fin = Date.parse(iso);
  if (Number.isNaN(fin)) return 0;
  return Math.max(0, Math.ceil((fin - ahoraServidor) / 1000));
}

// 75 -> "1:15"
export function formatoReloj(segundos: number): string {
  const m = Math.floor(segundos / 60);
  const s = String(segundos % 60).padStart(2, "0");
  return `${m}:${s}`;
}

// Decide si `nuevo` reemplaza al mercado en pantalla: es el mismo mercado
// (cambio de estado) o uno abierto despues.
export function reemplaza(nuevo: Mercado, actual: Mercado | null): boolean {
  if (!actual || nuevo.id === actual.id) return true;
  return Date.parse(nuevo.abre_en) >= Date.parse(actual.abre_en);
}

// Valida y limpia el formulario del admin con las mismas reglas del motor.
// Devuelve la peticion lista para enviar o el mensaje del primer error.
export function validarPeticion(
  pregunta: string,
  opciones: string[],
): { ok: true; peticion: PeticionMercado } | { ok: false; error: string } {
  const p = pregunta.trim();
  if (p === "") return { ok: false, error: "Escribe la pregunta del mercado" };
  if ([...p].length > MAX_LARGO_PREGUNTA) {
    return { ok: false, error: `La pregunta no puede tener más de ${MAX_LARGO_PREGUNTA} caracteres` };
  }

  const limpias = opciones.map((o) => o.trim());
  if (limpias.length < MIN_OPCIONES || limpias.length > MAX_OPCIONES) {
    return { ok: false, error: `El mercado debe tener entre ${MIN_OPCIONES} y ${MAX_OPCIONES} opciones` };
  }
  const vistas = new Set<string>();
  for (const o of limpias) {
    if (o === "") return { ok: false, error: "Ninguna opción puede estar vacía" };
    if ([...o].length > MAX_LARGO_OPCION) {
      return { ok: false, error: `Cada opción puede tener máximo ${MAX_LARGO_OPCION} caracteres` };
    }
    const clave = o.toLowerCase();
    if (vistas.has(clave)) return { ok: false, error: `La opción "${o}" está repetida` };
    vistas.add(clave);
  }
  return { ok: true, peticion: { pregunta: p, opciones: limpias } };
}
