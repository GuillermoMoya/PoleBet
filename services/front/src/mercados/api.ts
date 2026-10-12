// Peticiones REST de HU-11 al motor.

import { ErrorHttp, http } from "../api/http";
import type { Mercado, PeticionMercado } from "../api/tipos";
import { esMercado } from "./mercado";

// GET /mercados/actual: el mercado mas reciente, en el estado que este, o
// null si nunca se abrio uno. Un 404 (motor sin HU-11, p. ej. el mock) se
// trata igual que "no hay mercado".
export async function obtenerMercadoActual(signal?: AbortSignal): Promise<Mercado | null> {
  try {
    const m = await http.get<unknown>("motor", "/mercados/actual", { signal });
    return esMercado(m) ? m : null;
  } catch (error) {
    if (error instanceof ErrorHttp && error.status === 404) return null;
    throw error;
  }
}

// POST /mercados (solo admin). Lanza ErrorHttp con el mensaje del motor si
// lo rechaza: validacion (400), ya hay uno abierto (409), etc.
export async function abrirMercado(peticion: PeticionMercado): Promise<Mercado> {
  const m = await http.post<unknown>("motor", "/mercados", peticion);
  if (!esMercado(m)) throw new ErrorHttp(0, "El motor respondió con un mercado inválido.");
  return m;
}
