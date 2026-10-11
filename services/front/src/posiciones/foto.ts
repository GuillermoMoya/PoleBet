// Logica de HU-05 sin React, para poder probarla sola: validar una foto de
// posiciones, decidir si es mas nueva que otra y detectar que pilotos
// cambiaron de puesto.

import type { FotoPosiciones } from "../api/tipos";

// "sube" = gano posiciones (su numero de puesto bajo); "baja" = las perdio.
export type Movimiento = "sube" | "baja";

// Movimiento de cada piloto que cambio, por numero de carro.
export type Cambios = Map<number, Movimiento>;

// Revisa que lo recibido tenga la forma minima de una foto. Protege la
// pantalla de un mensaje mal formado.
export function esFoto(dato: unknown): dato is FotoPosiciones {
  if (typeof dato !== "object" || dato === null) return false;
  const { pilotos } = dato as { pilotos?: unknown };
  return (
    Array.isArray(pilotos) &&
    pilotos.every((p) => typeof p === "object" && p !== null && typeof p.posicion === "number" && typeof p.numero === "number")
  );
}

// Devuelve la foto con los pilotos ordenados por posicion.
export function ordenar(foto: FotoPosiciones): FotoPosiciones {
  return { ...foto, pilotos: [...foto.pilotos].sort((a, b) => a.posicion - b.posicion) };
}

// true si `nueva` no es mas vieja que `actual`. Evita que una respuesta REST
// que llega tarde pise un mensaje mas reciente del WebSocket. Si alguna fecha
// no se puede leer, se acepta la nueva.
export function esMasReciente(nueva: FotoPosiciones, actual: FotoPosiciones): boolean {
  const tNueva = Date.parse(nueva.actualizado_en);
  const tActual = Date.parse(actual.actualizado_en);
  if (Number.isNaN(tNueva) || Number.isNaN(tActual)) return true;
  return tNueva >= tActual;
}

// Compara dos fotos y dice que pilotos subieron o bajaron. Sin foto anterior
// (primera carga) no hay cambios que resaltar.
export function detectarCambios(anterior: FotoPosiciones | null, nueva: FotoPosiciones): Cambios {
  const cambios: Cambios = new Map();
  if (!anterior) return cambios;
  const antes = new Map(anterior.pilotos.map((p) => [p.numero, p.posicion]));
  for (const piloto of nueva.pilotos) {
    const previa = antes.get(piloto.numero);
    if (previa === undefined || previa === piloto.posicion) continue;
    cambios.set(piloto.numero, piloto.posicion < previa ? "sube" : "baja");
  }
  return cambios;
}

// Latencia en ms entre que el motor emitio el mensaje y ahora (RNF-04).
// null si la foto no trae emitido_en.
export function latenciaDe(foto: FotoPosiciones, ahora: number = Date.now()): number | null {
  if (!foto.emitido_en) return null;
  const emitido = Date.parse(foto.emitido_en);
  return Number.isNaN(emitido) ? null : Math.max(0, ahora - emitido);
}
