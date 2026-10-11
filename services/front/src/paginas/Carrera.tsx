// Pantalla /carrera: la carrera en vivo. Sigue el mockup "Carrera en vivo":
// cabecera, posiciones a la izquierda (HU-05) y el mercado en el centro
// (HU-06 y HU-08).

import { MOSTRAR_LATENCIA } from "../api/config";
import { useWs } from "../api/WsContexto";
import DemoApuesta from "../apuestas/DemoApuesta.jsx";
import AvisoConexion from "../posiciones/AvisoConexion";
import PanelPosiciones from "../posiciones/PanelPosiciones";
import { usePosiciones } from "../posiciones/usePosiciones";
import estilos from "./Carrera.module.css";

export default function Carrera() {
  const { estado } = useWs();
  const posiciones = usePosiciones();
  const { foto, fase, latenciaMs } = posiciones;
  const enVivo = fase === "lista" && estado === "abierta";

  return (
    <>
      {/* Cabecera: estado de la carrera y, si se pide, la latencia (RNF-04) */}
      <section className={`tarjeta ${estilos.cabecera}`}>
        <div className={estilos.cabeceraTitulo}>
          <span className={enVivo ? "etiqueta" : `etiqueta ${estilos.etiquetaApagada}`}>
            <span className={estilos.puntoVivo} aria-hidden="true" />
            {enVivo ? "En vivo" : "Sin señal"}
          </span>
          <div>
            <h1 className="titulo-pagina" style={{ fontSize: 30 }}>
              Carrera en vivo
            </h1>
            <span className="texto-secundario">
              {foto ? [foto.vuelta !== undefined && `Vuelta ${foto.vuelta}`, foto.carrera_id].filter(Boolean).join(" · ") : "Esperando datos de la carrera"}
            </span>
          </div>
        </div>
        {MOSTRAR_LATENCIA && (
          <span className={estilos.latencia} title="Tiempo entre que el motor emitió el último mensaje y llegó a esta pantalla">
            Latencia: <b>{latenciaMs === null ? "—" : `${latenciaMs} ms`}</b>
          </span>
        )}
      </section>

      <AvisoConexion estado={estado} />

      <div className={estilos.columnas}>
        {/* Columna izquierda: HU-05 */}
        <aside className={estilos.lateral}>
          <PanelPosiciones posiciones={posiciones} />
        </aside>

        {/* Columna central: mercado activo */}
        <div className={estilos.centro}>
          {/* HU-06: por ahora la demo con datos simulados. Va en una tarjeta
              clara porque el formulario aun no usa el tema oscuro.
              PENDIENTE HU-06/HU-11: reemplazar por el mercado real. */}
          <section className="tarjeta-clara">
            <DemoApuesta />
          </section>

          {/* PENDIENTE HU-08: resultado de la apuesta al liquidar el mercado. */}
          <section className="tarjeta">
            <h2 className="titulo-seccion">Resultado de tu apuesta</h2>
            <p className="texto-secundario" style={{ margin: 0 }}>
              Pendiente: HU-08. Aquí se verá si acertaste y los puntos obtenidos cuando se liquide el mercado.
            </p>
          </section>
        </div>
      </div>
    </>
  );
}
