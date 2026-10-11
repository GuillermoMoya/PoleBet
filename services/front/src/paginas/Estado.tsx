// Pantalla /estado: diagnostico de la infraestructura. Verifica que el front
// se comunica con los dos backends por REST y con el motor por WebSocket.
// (Era la pantalla inicial del esqueleto; se conserva para la demo de HU-10.)

import { useEffect, useState } from "react";
import { http, type Servicio } from "../api/http";
import { useWs } from "../api/WsContexto";

interface Salud {
  ok: boolean;
  texto: string;
}

// Consulta GET /health del servicio y devuelve su respuesta como texto.
function useSalud(servicio: Servicio): Salud {
  const [salud, setSalud] = useState<Salud>({ ok: false, texto: "cargando..." });

  useEffect(() => {
    let vivo = true;
    http
      .get<unknown>(servicio, "/health")
      .then((datos) => vivo && setSalud({ ok: true, texto: JSON.stringify(datos) }))
      .catch((error: Error) => vivo && setSalud({ ok: false, texto: `error: ${error.message}` }));
    return () => {
      vivo = false;
    };
  }, [servicio]);

  return salud;
}

// Tarjeta con el estado de una pieza de la infraestructura.
function Tarjeta({ titulo, ok, texto }: { titulo: string; ok: boolean; texto: string }) {
  return (
    <section className="tarjeta">
      <h2 className="titulo-seccion">
        {titulo} {ok ? "✅" : "⏳"}
      </h2>
      <code style={{ wordBreak: "break-all" }}>{texto}</code>
    </section>
  );
}

export default function Estado() {
  const pagos = useSalud("pagos");
  const motor = useSalud("motor");
  const { estado } = useWs();

  return (
    <>
      <h1 className="titulo-pagina">Estado del sistema</h1>
      <Tarjeta titulo="Pagos y usuarios" ok={pagos.ok} texto={pagos.texto} />
      <Tarjeta titulo="Motor de apuestas" ok={motor.ok} texto={motor.texto} />
      {/* El WebSocket solo se abre con sesion: sin ella queda "cerrada". */}
      <Tarjeta titulo="WebSocket (motor)" ok={estado === "abierta"} texto={`conexión ${estado}`} />
    </>
  );
}
