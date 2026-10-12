// Pantalla /admin (solo administrador, HU-12): abrir mercados (HU-11).
//
// Criterios de aceptacion que cubre:
//   - El admin abre el mercado; los jugadores lo reciben al instante por
//     WebSocket (lo difunde el motor). En el prototipo hay un solo tipo de
//     mercado (Si/No, "¿habra algun cambio de posicion...?"), asi que la
//     pregunta y las opciones vienen prellenadas y en SOLO LECTURA. El
//     contrato de POST /mercados no cambia ({pregunta, opciones}); el motor
//     rechaza con 400 opciones distintas de ["Sí", "No"]. Ver mercado.ts.
//   - Arriba ve el mercado actual con su estado y cuenta regresiva:
//     Abierto → Cerrado (en observacion) → Liquidado.
//   - El cierre lo decide el reloj del servidor (RNF-05): aqui solo se muestra.
//   - Mientras haya un mercado abierto no se puede abrir otro (el motor
//     tambien lo rechaza con 409).

import { useState, type FormEvent } from "react";
import { ErrorHttp } from "../api/http";
import { abrirMercado } from "../mercados/api";
import EstadoMercado from "../mercados/EstadoMercado";
import { faseDe, OPCIONES_SI_NO, PREGUNTA_CAMBIO_POSICION, validarPeticion } from "../mercados/mercado";
import { useMercado, useRelojServidor } from "../mercados/useMercado";
import estilos from "./Admin.module.css";

interface Mensaje {
  ok: boolean;
  texto: string;
}

// Pregunta y opciones fijas del unico tipo de mercado (solo lectura).
const PREGUNTA = PREGUNTA_CAMBIO_POSICION;
const OPCIONES = [...OPCIONES_SI_NO];

export default function Admin() {
  const { mercado, cargando, error, desfaseMs, aplicar, recargar } = useMercado();
  const ahora = useRelojServidor(desfaseMs);

  const [enviando, setEnviando] = useState(false);
  const [mensaje, setMensaje] = useState<Mensaje | null>(null);

  const hayAbierto = mercado !== null && faseDe(mercado, ahora) === "abierto";
  // Se sigue validando con las mismas reglas del motor por si las constantes
  // cambian algun dia; hoy siempre es valida.
  const validacion = validarPeticion(PREGUNTA, OPCIONES);
  const puedeAbrir = validacion.ok && !enviando && !hayAbierto;

  async function onSubmit(evento: FormEvent<HTMLFormElement>) {
    evento.preventDefault();
    if (!puedeAbrir || !validacion.ok) return; // tambien evita el doble envio
    setEnviando(true);
    setMensaje(null);
    try {
      const nuevo = await abrirMercado(validacion.peticion);
      aplicar(nuevo); // el mensaje del WebSocket llega tambien; reemplaza() evita duplicar
      setMensaje({ ok: true, texto: "Mercado abierto. Los jugadores conectados ya lo están viendo." });
    } catch (causa) {
      setMensaje({ ok: false, texto: causa instanceof ErrorHttp ? causa.message : "Ocurrió un error inesperado." });
    } finally {
      setEnviando(false);
    }
  }

  return (
    <div className={estilos.pagina}>
      <h1 className="titulo-pagina">Panel de administración</h1>

      {/* Mercado actual */}
      <section className="tarjeta" aria-labelledby="titulo-actual">
        <h2 id="titulo-actual" className="titulo-seccion">
          Mercado actual
        </h2>
        {error ? (
          <div role="alert" className={estilos.error}>
            <p>{error}</p>
            <button type="button" className="boton boton-secundario" onClick={recargar}>
              Reintentar
            </button>
          </div>
        ) : mercado ? (
          <EstadoMercado mercado={mercado} ahoraServidor={ahora} mostrarOpciones />
        ) : (
          <p className="texto-secundario">{cargando ? "Cargando…" : "Todavía no se ha abierto ningún mercado."}</p>
        )}
      </section>

      {/* Abrir un mercado */}
      <section className="tarjeta" aria-labelledby="titulo-abrir">
        <h2 id="titulo-abrir" className="titulo-seccion">
          Abrir un mercado
        </h2>
        <form className={estilos.formulario} onSubmit={onSubmit} noValidate>
          <div className={estilos.campo}>
            <label htmlFor="pregunta">Pregunta</label>
            <input id="pregunta" value={PREGUNTA} readOnly />
          </div>

          <fieldset className={estilos.opciones}>
            <legend>Opciones</legend>
            {OPCIONES.map((o, i) => (
              <div key={o} className={estilos.opcion}>
                <label htmlFor={`opcion-${i}`} className="solo-lectores">
                  Opción {i + 1}
                </label>
                <input id={`opcion-${i}`} value={o} readOnly />
              </div>
            ))}
          </fieldset>

          <p className="texto-secundario">
            Gana «Sí» si la carrera registra al menos un cambio de posición durante la ventana de observación; si no, gana
            «No».
          </p>

          <p className="texto-secundario">
            Al abrirlo empieza la ventana de apuestas. El servidor lo cierra solo al terminarla y, tras la ventana de observación, lo
            liquida.
          </p>

          {!validacion.ok && <p className={estilos.aviso}>{validacion.error}</p>}
          {hayAbierto && <p className={estilos.aviso}>Ya hay un mercado abierto: espera a que cierre para abrir otro.</p>}

          <button type="submit" className="boton" disabled={!puedeAbrir}>
            {enviando ? "Abriendo…" : "Abrir mercado"}
          </button>

          <div aria-live="polite">
            {mensaje &&
              (mensaje.ok ? (
                <p role="status" className={estilos.ok}>
                  {mensaje.texto}
                </p>
              ) : (
                <p role="alert" className={estilos.error}>
                  {mensaje.texto}
                </p>
              ))}
          </div>
        </form>
      </section>
    </div>
  );
}
