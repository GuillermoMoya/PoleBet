// Pantalla /login. PENDIENTE HU-02: reemplazar el contenido por el formulario
// real. Mientras tanto ofrece entrar con un usuario simulado, para poder
// navegar el resto de la app.

import { useLocation, useNavigate } from "react-router-dom";
import { USUARIOS_SIMULADOS, useAuth, type Rol } from "../auth/AuthContext";
import Pendiente from "../componentes/Pendiente";

export default function Login() {
  const { iniciarSesion } = useAuth();
  const navegar = useNavigate();
  const ubicacion = useLocation();

  // Inicia la sesion simulada y vuelve a la pantalla que se queria ver.
  function entrarComo(rol: Rol) {
    iniciarSesion(USUARIOS_SIMULADOS[rol]);
    const desde = (ubicacion.state as { desde?: string } | null)?.desde;
    navegar(desde ?? "/", { replace: true });
  }

  return (
    <Pendiente titulo="Iniciar sesión" hu="HU-02" detalle="Aquí va el formulario de correo y contraseña.">
      <div style={{ display: "flex", flexWrap: "wrap", gap: 12 }}>
        <button type="button" className="boton" onClick={() => entrarComo("player")}>
          Entrar como jugador (simulado)
        </button>
        <button type="button" className="boton boton-secundario" onClick={() => entrarComo("admin")}>
          Entrar como admin (simulado)
        </button>
      </div>
    </Pendiente>
  );
}
