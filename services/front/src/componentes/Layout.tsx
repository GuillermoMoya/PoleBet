// Marco comun de todas las pantallas: header (logo, navegacion, saldo y
// usuario) y debajo el contenido de la ruta actual.

import { Link, NavLink, Outlet } from "react-router-dom";
import { useAuth, type Rol } from "../auth/AuthContext";
import estilos from "./Layout.module.css";

interface Enlace {
  a: string;
  texto: string;
  roles: Rol[]; // roles que ven el enlace
}

// Enlaces del menu. Cada HU que agregue una pantalla la registra aqui.
const ENLACES: Enlace[] = [
  { a: "/carrera", texto: "En vivo", roles: ["player"] },
  { a: "/billetera", texto: "Billetera", roles: ["player"] },
  { a: "/admin", texto: "Admin", roles: ["admin"] },
];

// Iniciales para el circulo del usuario: "Jugador Demo" -> "JD".
function iniciales(nombre: string): string {
  return nombre
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((parte) => parte[0].toUpperCase())
    .join("");
}

export default function Layout() {
  const { usuario, cerrarSesion, avisoAcceso, descartarAviso } = useAuth();
  const enlaces = usuario ? ENLACES.filter((e) => e.roles.includes(usuario.rol)) : [];

  return (
    <div className={estilos.app}>
      <header className={estilos.header}>
        <div className={estilos.izquierda}>
          {/* Logo: barra roja inclinada + nombre */}
          <Link to="/" className={estilos.logo} aria-label="PoleBet, inicio">
            <span className={estilos.logoBarra} aria-hidden="true" />
            <span className={estilos.logoTexto}>
              POLE<span className={estilos.logoAcento}>BET</span>
            </span>
          </Link>

          {/* Navegacion principal, segun el rol */}
          {enlaces.length > 0 && (
            <nav className={estilos.nav} aria-label="Principal">
              {enlaces.map((e) => (
                <NavLink
                  key={e.a}
                  to={e.a}
                  className={({ isActive }) => (isActive ? `${estilos.enlace} ${estilos.enlaceActivo}` : estilos.enlace)}
                >
                  {e.texto}
                </NavLink>
              ))}
            </nav>
          )}
        </div>

        {/* Lado derecho: saldo y usuario, o los accesos de quien no tiene sesion */}
        <div className={estilos.derecha}>
          {usuario ? (
            <>
              {usuario.rol === "player" && (
                // PENDIENTE HU-03: mostrar el saldo real de la billetera.
                <Link to="/billetera" className={estilos.saldo} title="Saldo pendiente: HU-03">
                  <span className={estilos.saldoRombo} aria-hidden="true" />— tokens
                </Link>
              )}
              <span className={estilos.avatar} title={usuario.nombre}>
                {iniciales(usuario.nombre)}
              </span>
              <button type="button" className={estilos.salir} onClick={cerrarSesion}>
                Salir
              </button>
            </>
          ) : (
            <>
              <Link to="/login" className={estilos.enlace}>
                Ingresar
              </Link>
              <Link to="/registro" className="boton">
                Registrarme
              </Link>
            </>
          )}
        </div>
      </header>

      {/* Aviso global de acceso denegado (lo dispara un 403 en http.ts) */}
      {avisoAcceso && (
        <div className={estilos.aviso} role="alert">
          <span>{avisoAcceso}</span>
          <button type="button" className={estilos.avisoCerrar} onClick={descartarAviso}>
            Cerrar
          </button>
        </div>
      )}

      <main className={estilos.contenido}>
        <Outlet />
      </main>
    </div>
  );
}
