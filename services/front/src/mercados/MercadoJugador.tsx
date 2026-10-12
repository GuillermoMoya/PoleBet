// Mercado en la pantalla del jugador (/carrera).
//
//   - Sin mercado: avisa que hay que esperar al administrador.
//   - Abierto: estado + formulario de apuesta de HU-06.
//   - Cerrado / liquidado: estado, pregunta y opciones, sin formulario.
//
// Aparece y cambia de estado solo, por WebSocket (useMercado), sin recargar.

import type { ComponentType } from "react";
import type { Mercado } from "../api/tipos";
import FormularioApuestaJs from "../apuestas/FormularioApuesta.jsx";
import { useBilletera } from "../billetera/BilleteraContexto";
import EstadoMercado from "./EstadoMercado";
import { faseDe } from "./mercado";
import { useMercado, useRelojServidor } from "./useMercado";

// FormularioApuesta (HU-06) esta en JavaScript; TypeScript infiere mal sus
// props (saldo = null lo deja como tipo null). Se declaran aqui.
const FormularioApuesta = FormularioApuestaJs as unknown as ComponentType<{
  mercado: Pick<Mercado, "id" | "pregunta" | "opciones" | "cierra_en">;
  saldo?: number | null;
  onAceptada?: (apuesta: unknown) => void;
}>;

// FormularioApuesta cuenta con el reloj del navegador. Se le pasa cierra_en
// ya corrido por el desfase, para que su cuenta coincida con la del servidor.
function enRelojLocal(m: Mercado, desfaseMs: number): Mercado {
  return { ...m, cierra_en: new Date(Date.parse(m.cierra_en) - desfaseMs).toISOString() };
}

export default function MercadoJugador() {
  const { mercado, cargando, error, desfaseMs, recargar } = useMercado();
  const ahora = useRelojServidor(desfaseMs);
  const { saldo, recargar: recargarSaldo } = useBilletera();

  let contenido;
  if (error) {
    contenido = (
      <div role="alert">
        <p style={{ marginTop: 0 }}>{error}</p>
        <button type="button" className="boton boton-secundario" onClick={recargar}>
          Reintentar
        </button>
      </div>
    );
  } else if (!mercado) {
    contenido = (
      <p className="texto-secundario" style={{ margin: 0 }}>
        {cargando ? "Cargando mercado…" : "No hay ningún mercado abierto. Cuando el administrador abra uno, aparecerá aquí al instante."}
      </p>
    );
  } else if (faseDe(mercado, ahora) === "abierto") {
    contenido = (
      <>
        <EstadoMercado mercado={mercado} ahoraServidor={ahora} mostrarPregunta={false} mostrarCuenta={false} />
        {/* El formulario aun no usa el tema oscuro: va en una tarjeta clara. */}
        <div className="tarjeta-clara" style={{ marginTop: 16 }}>
          <FormularioApuesta mercado={enRelojLocal(mercado, desfaseMs)} saldo={saldo} onAceptada={recargarSaldo} />
        </div>
      </>
    );
  } else {
    contenido = <EstadoMercado mercado={mercado} ahoraServidor={ahora} mostrarOpciones />;
  }

  return (
    <section className="tarjeta" aria-labelledby="titulo-mercado">
      <h2 id="titulo-mercado" className="titulo-seccion">
        Mercado
      </h2>
      {contenido}
    </section>
  );
}
