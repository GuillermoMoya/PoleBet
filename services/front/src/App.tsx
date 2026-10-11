// Rutas de la app. Cada historia de usuario tiene su pantalla en src/paginas/;
// para completar una, se reemplaza el contenido de su archivo.
//
//   /registro   HU-01            publica
//   /login      HU-02            publica
//   /carrera    HU-05/06/08      jugador
//   /billetera  HU-03            jugador
//   /admin      HU-11/12         solo admin
//   /estado     diagnostico      publica

import { Navigate, Route, Routes } from "react-router-dom";
import { useAuth } from "./auth/AuthContext";
import RutaProtegida from "./auth/RutaProtegida";
import Layout from "./componentes/Layout";
import Admin from "./paginas/Admin";
import Billetera from "./paginas/Billetera";
import Carrera from "./paginas/Carrera";
import Estado from "./paginas/Estado";
import Login from "./paginas/Login";
import NoEncontrada from "./paginas/NoEncontrada";
import Registro from "./paginas/Registro";

// Pantalla de inicio segun quien entra: el jugador va a la carrera, el admin
// a su panel y quien no tiene sesion al login.
function Inicio() {
  const { usuario } = useAuth();
  if (!usuario) return <Navigate to="/login" replace />;
  return <Navigate to={usuario.rol === "admin" ? "/admin" : "/carrera"} replace />;
}

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Inicio />} />

        {/* Publicas */}
        <Route path="/registro" element={<Registro />} />
        <Route path="/login" element={<Login />} />
        <Route path="/estado" element={<Estado />} />

        {/* Solo jugador */}
        <Route element={<RutaProtegida roles={["player"]} />}>
          <Route path="/carrera" element={<Carrera />} />
          <Route path="/billetera" element={<Billetera />} />
        </Route>

        {/* Solo admin */}
        <Route element={<RutaProtegida roles={["admin"]} />}>
          <Route path="/admin" element={<Admin />} />
        </Route>

        <Route path="*" element={<NoEncontrada />} />
      </Route>
    </Routes>
  );
}
