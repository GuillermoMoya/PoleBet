// Aviso del estado del WebSocket (HU-05, criterio 3). Con la conexion abierta
// no muestra nada; mientras se conecta o se reconecta, avisa al jugador de que
// los datos pueden no estar al dia.

import type { EstadoWs } from "../api/ws";
import estilos from "./AvisoConexion.module.css";

// Texto de cada estado. "abierta" no tiene: no se muestra aviso.
const TEXTOS: Record<EstadoWs, string | null> = {
  conectando: "Conectando con la carrera…",
  abierta: null,
  reconectando: "Reconectando…",
  cerrada: "Sin conexión en vivo",
};

interface Props {
  estado: EstadoWs;
}

export default function AvisoConexion({ estado }: Props) {
  const texto = TEXTOS[estado];
  // El contenedor siempre existe para que el lector de pantalla anuncie los
  // cambios (role="status"). Sin texto se saca del flujo, para que no deje un
  // hueco en la pantalla.
  return (
    <div role="status" aria-live="polite" className={texto ? undefined : "solo-lectores"}>
      {texto && (
        <p className={estilos.aviso}>
          <span className={estilos.punto} aria-hidden="true" />
          {texto}
        </p>
      )}
    </div>
  );
}
