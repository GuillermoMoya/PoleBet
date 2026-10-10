// Tarjeta "Posiciones" de la pantalla de carrera: decide que mostrar segun la
// fase (cargando, sin carrera, error o la tabla).

import TablaPosiciones from "./TablaPosiciones";
import type { EstadoPosiciones } from "./usePosiciones";

interface Props {
  posiciones: EstadoPosiciones;
}

export default function PanelPosiciones({ posiciones }: Props) {
  const { fase, foto, cambios, error, reintentar } = posiciones;

  return (
    <section className="tarjeta" aria-labelledby="titulo-posiciones">
      <h2 id="titulo-posiciones" className="titulo-seccion">
        Posiciones
      </h2>

      {fase === "cargando" && (
        <p className="texto-secundario" role="status">
          Cargando posiciones…
        </p>
      )}

      {fase === "vacia" && <p className="texto-secundario">No hay una carrera en curso.</p>}

      {fase === "error" && (
        <div role="alert" style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: 12 }}>
          <p style={{ margin: 0 }}>{error}</p>
          <button type="button" className="boton boton-secundario" onClick={reintentar}>
            Reintentar
          </button>
        </div>
      )}

      {fase === "lista" && foto && <TablaPosiciones pilotos={foto.pilotos} cambios={cambios} />}
    </section>
  );
}
