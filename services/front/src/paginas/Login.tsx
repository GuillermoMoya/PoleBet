// Pantalla /login (HU-02): formulario de correo y contraseña.
//
// Criterios de aceptacion que cubre:
//   - Credenciales correctas: Pagos pone la cookie con el JWT y se entra a la
//     app (el jugador a /carrera, el admin a /admin).
//   - Credenciales incorrectas: «Correo o contraseña incorrectos», sin decir
//     cual de los dos fallo.
//   - Si ya hay sesion, /login no tiene sentido y se va al inicio.

import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";
import { ErrorHttp } from "../api/http";
import { useAuth } from "../auth/AuthContext";
import { destinoTrasLogin } from "../auth/destino";
import estilos from "./Login.module.css";

// Errores de validacion por campo, antes de llamar a Pagos.
interface ErroresCampos {
  email?: string;
  password?: string;
}

function validar(email: string, password: string): ErroresCampos {
  const errores: ErroresCampos = {};
  if (!email.trim()) errores.email = "Escribe tu correo.";
  if (!password) errores.password = "Escribe tu contraseña.";
  return errores;
}

export default function Login() {
  const { usuario, cargando, iniciarSesion } = useAuth();
  const navegar = useNavigate();
  const ubicacion = useLocation();
  const desde = (ubicacion.state as { desde?: string } | null)?.desde;

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errores, setErrores] = useState<ErroresCampos>({});
  const [errorGeneral, setErrorGeneral] = useState<string | null>(null);
  const [enviando, setEnviando] = useState(false);

  // Mientras se averigua si ya hay sesion, no se muestra el formulario.
  if (cargando) return <p role="status">Cargando…</p>;
  // Ya hay sesion (p. ej. abrio /login desde el historial): al inicio.
  // Durante el envio no se redirige aqui: lo hace onSubmit con el destino correcto.
  if (usuario && !enviando) return <Navigate to={destinoTrasLogin(usuario.rol, desde)} replace />;

  async function onSubmit(evento: FormEvent<HTMLFormElement>) {
    evento.preventDefault();
    if (enviando) return; // evita doble envio con doble clic

    const nuevos = validar(email, password);
    setErrores(nuevos);
    setErrorGeneral(null);
    if (nuevos.email || nuevos.password) return;

    setEnviando(true);
    try {
      const nuevo = await iniciarSesion(email.trim(), password);
      navegar(destinoTrasLogin(nuevo.rol, desde), { replace: true });
    } catch (error) {
      // ErrorHttp ya trae el mensaje en español: credenciales (401), red
      // caida (status 0) o error del servidor.
      setErrorGeneral(error instanceof ErrorHttp ? error.message : "Ocurrió un error inesperado.");
      setPassword(""); // no dejar la contraseña escrita tras un fallo
      setEnviando(false);
    }
  }

  return (
    <section className={`tarjeta ${estilos.contenedor}`}>
      <h1 className="titulo-pagina">Iniciar sesión</h1>
      <p className="texto-secundario">Entra con el correo y la contraseña de tu cuenta.</p>

      {errorGeneral && (
        <p className={estilos.error} role="alert">
          {errorGeneral}
        </p>
      )}

      {/* noValidate: los mensajes los pone la app, en español y por campo. */}
      <form className={estilos.formulario} onSubmit={onSubmit} noValidate>
        <div className={estilos.campo}>
          <label htmlFor="login-email">Correo</label>
          <input
            id="login-email"
            type="email"
            autoComplete="email"
            inputMode="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={errores.email ? true : undefined}
            aria-describedby={errores.email ? "login-email-error" : undefined}
            disabled={enviando}
          />
          {errores.email && (
            <span id="login-email-error" className={estilos.errorCampo}>
              {errores.email}
            </span>
          )}
        </div>

        <div className={estilos.campo}>
          <label htmlFor="login-password">Contraseña</label>
          <input
            id="login-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={errores.password ? true : undefined}
            aria-describedby={errores.password ? "login-password-error" : undefined}
            disabled={enviando}
          />
          {errores.password && (
            <span id="login-password-error" className={estilos.errorCampo}>
              {errores.password}
            </span>
          )}
        </div>

        <button type="submit" className="boton" disabled={enviando}>
          {enviando ? "Ingresando…" : "Ingresar"}
        </button>
      </form>

      <p className="texto-secundario">
        ¿No tienes cuenta? <Link to="/registro">Regístrate</Link>
      </p>
    </section>
  );
}
