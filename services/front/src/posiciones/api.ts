// Peticiones REST de HU-05.

import { ErrorHttp, http } from "../api/http";
import type { FotoPosiciones } from "../api/tipos";
import { esFoto } from "./foto";

// Pide al motor la ultima foto de posiciones (GET /posiciones). Devuelve null
// si no hay una carrera en curso (404 o respuesta sin pilotos).
export async function obtenerPosiciones(signal?: AbortSignal): Promise<FotoPosiciones | null> {
  try {
    const foto = await http.get<unknown>("motor", "/posiciones", { signal });
    return esFoto(foto) && foto.pilotos.length > 0 ? foto : null;
  } catch (error) {
    if (error instanceof ErrorHttp && error.status === 404) return null;
    throw error;
  }
}
