// Sesion del usuario para toda la app.
//
// ESQUELETO: mientras no exista el login (HU-02) la sesion es simulada.
// PENDIENTE HU-02: al montar, preguntarle a Pagos quien es el usuario de la
// cookie (p. ej. http.get<Usuario>("pagos", "/auth/me")), y hacer que
// iniciarSesion y cerrarSesion llamen a los endpoints reales. El resto de la
// app solo usa useAuth(), asi que no hay que tocar nada mas.

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { configurarHttp } from "../api/http";

// Roles como los define el modelo de datos de Pagos y el contrato del JWT.
export type Rol = "player" | "admin";

export interface Usuario {
  id: string; // UUID (claim "sub" del JWT)
  nombre: string;
  rol: Rol;
}

export interface ValorAuth {
  usuario: Usuario | null;
  cargando: boolean; // true mientras se averigua si hay sesion
  iniciarSesion: (usuario: Usuario) => void;
  cerrarSesion: () => void;
  avisoAcceso: string | null; // mensaje de "acceso denegado" (403)
  descartarAviso: () => void;
}

// Usuarios de mentira, uno por rol.
export const USUARIOS_SIMULADOS: Record<Rol, Usuario> = {
  player: { id: "00000000-0000-4000-8000-000000000001", nombre: "Jugador Demo", rol: "player" },
  admin: { id: "00000000-0000-4000-8000-000000000002", nombre: "Admin Demo", rol: "admin" },
};

// Decide con que usuario arranca la app segun VITE_ROL_SIMULADO
// (player por defecto; "ninguno" arranca sin sesion).
function usuarioInicial(): Usuario | null {
  const rol = import.meta.env.VITE_ROL_SIMULADO ?? "player";
  return rol === "player" || rol === "admin" ? USUARIOS_SIMULADOS[rol] : null;
}

const AuthContexto = createContext<ValorAuth | null>(null);

interface Props {
  children: ReactNode;
  usuarioInicial?: Usuario | null; // fija la sesion en las pruebas
}

export function AuthProvider({ children, usuarioInicial: inicial }: Props) {
  const [usuario, setUsuario] = useState<Usuario | null>(() => (inicial !== undefined ? inicial : usuarioInicial()));
  const [avisoAcceso, setAvisoAcceso] = useState<string | null>(null);

  const iniciarSesion = useCallback((nuevo: Usuario) => setUsuario(nuevo), []);
  const cerrarSesion = useCallback(() => setUsuario(null), []);
  const descartarAviso = useCallback(() => setAvisoAcceso(null), []);

  // Conecta http.ts con la sesion: un 401 la limpia (RutaProtegida lleva
  // entonces a /login) y un 403 deja el aviso de acceso denegado.
  useEffect(() => {
    configurarHttp({ sesionVencida: cerrarSesion, accesoDenegado: setAvisoAcceso });
    return () => configurarHttp({});
  }, [cerrarSesion]);

  const valor = useMemo<ValorAuth>(
    () => ({ usuario, cargando: false, iniciarSesion, cerrarSesion, avisoAcceso, descartarAviso }),
    [usuario, iniciarSesion, cerrarSesion, avisoAcceso, descartarAviso],
  );

  return <AuthContexto.Provider value={valor}>{children}</AuthContexto.Provider>;
}

// Hook para leer la sesion.
export function useAuth(): ValorAuth {
  const valor = useContext(AuthContexto);
  if (!valor) throw new Error("useAuth debe usarse dentro de <AuthProvider>");
  return valor;
}
