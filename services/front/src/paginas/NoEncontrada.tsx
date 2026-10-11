// Pantalla para cualquier ruta que no existe.

import { Link } from "react-router-dom";
import Pendiente from "../componentes/Pendiente";

export default function NoEncontrada() {
  return (
    <Pendiente titulo="Página no encontrada" detalle="La dirección que abriste no existe.">
      <p style={{ margin: 0 }}>
        <Link to="/" className="boton">
          Volver al inicio
        </Link>
      </p>
    </Pendiente>
  );
}
