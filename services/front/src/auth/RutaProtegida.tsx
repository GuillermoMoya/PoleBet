// Protege un grupo de rutas por sesion y, si se indica, por rol.
//
//   <Route element={<RutaProtegida roles={["admin"]} />}>
//     <Route path="/admin" element={<Admin />} />
//   </Route>

import { Navigate, Outlet, useLocation } from "react-router-dom";
import Pendiente from "../componentes/Pendiente";
import { useAuth, type Rol } from "./AuthContext";

interface Props {
  roles?: Rol[]; // si se omite, basta con tener sesion
}

export default function RutaProtegida({ roles }: Props) {
  const { usuario, cargando } = useAuth();
  const ubicacion = useLocation();

  // Mientras se averigua la sesion no se decide nada, para no mandar a /login
  // a alguien que si tiene sesion.
  if (cargando) return <p role="status">Cargando…</p>;

  // Sin sesion: a /login, recordando a donde queria ir.
  if (!usuario) return <Navigate to="/login" replace state={{ desde: ubicacion.pathname }} />;

  // Con sesion pero sin el rol: se explica en vez de redirigir.
  if (roles && !roles.includes(usuario.rol)) {
    return <Pendiente titulo="Acceso denegado" detalle="Tu usuario no tiene permiso para ver esta página." />;
  }

  return <Outlet />;
}
