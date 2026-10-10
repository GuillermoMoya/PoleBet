// A donde mandar al usuario despues de iniciar sesion.
//
// Si llego a /login porque intento abrir una ruta protegida, se le devuelve
// ahi (RutaProtegida guarda esa ruta en `state.desde`). Pero solo si su rol
// puede verla: un admin que venia de /carrera iria a "Acceso denegado", asi
// que en ese caso se le manda a su pantalla de inicio.

import type { Rol } from "./AuthContext";

const INICIO: Record<Rol, string> = { player: "/carrera", admin: "/admin" };

// Prefijos de ruta exclusivos de cada rol (mismo reparto que App.tsx).
const SOLO_ADMIN = ["/admin"];
const SOLO_JUGADOR = ["/carrera", "/billetera"];

function empiezaCon(ruta: string, prefijos: string[]): boolean {
  return prefijos.some((p) => ruta === p || ruta.startsWith(`${p}/`));
}

export function destinoTrasLogin(rol: Rol, desde?: string | null): string {
  // Sin ruta previa, o con una que no es interna (evita redirecciones
  // abiertas tipo "//otro-sitio.com"), se va al inicio del rol.
  if (!desde || !desde.startsWith("/") || desde.startsWith("//") || desde === "/login") return INICIO[rol];
  if (rol === "admin" && empiezaCon(desde, SOLO_JUGADOR)) return INICIO.admin;
  if (rol === "player" && empiezaCon(desde, SOLO_ADMIN)) return INICIO.player;
  return desde;
}
