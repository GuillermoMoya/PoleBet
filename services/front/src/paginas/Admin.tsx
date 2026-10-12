// Pantalla /admin (solo administrador, HU-12): abrir mercados (HU-11).
//
// Criterios de aceptacion que cubre:
//   - El admin escribe la pregunta y las opciones y abre el mercado; los
//     jugadores lo reciben al instante por WebSocket (lo difunde el motor).
//   - Arriba ve el mercado actual con su estado y cuenta regresiva:
//     Abierto → Cerrado (en observacion) → Liquidado.
//   - El cierre lo decide el reloj del servidor (RNF-05): aqui solo se muestra.
//   - Mientras haya un mercado abierto no se puede abrir otro (el motor
//     tambien lo rechaza con 409).

import { useState, type FormEvent } from "react";
import { ErrorHttp } from "../api/http";
import { abrirMercado } from "../mercados/api";
import EstadoMercado from "../mercados/EstadoMercado";
import { faseDe, MAX_LARGO_OPCION, MAX_LARGO_PREGUNTA, MAX_OPCIONES, MIN_OPCIONES, validarPeticion } from "../mercados/mercado";
import { useMercado, useRelojServidor } from "../mercados/useMercado";
import estilos from "./Admin.module.css";

interface Mensaje {
  ok: boolean;
  texto: string;
}

const VACIAS = () => Array.from({ length: MIN_OPCIONES }, () => "");

export default function Admin() {
  const { mercado, cargando, error, desfaseMs, aplicar, recargar } = useMercado();
  const ahora = useRelojServidor(desfaseMs);

  const [pregunta, setPregunta] = useState("");
  const [opciones, setOpciones] = useState<string[]>(VACIAS);
  const [enviando, setEnviando] = useState(false);
  const [mensaje, setMensaje] = useState<Mensaje | null>(null);

  const hayAbierto = mercado !== null && faseDe(mercado, ahora) === "abierto";
  const validacion = validarPeticion(pregunta, opciones);
  const puedeAbrir = validacion.ok && !enviando && !hayAbierto;

  function cambiarOpcion(i: number, valor: string) {
    setOpciones((o) => o.map((x, j) => (j === i ? valor : x)));
    setMensaje(null);
  }

  async function onSubmit(evento: FormEvent<HTMLFormElement>) {
    evento.preventDefault();
    if (!puedeAbrir || !validacion.ok) return; // tambien evita el doble envio
    setEnviando(true);
    setMensaje(null);
    try {
      const nuevo = await abrirMercado(validacion.peticion);
      aplicar(nuevo); // el mensaje del WebSocket llega tambien; reemplaza() evita duplicar
      setPregunta("");
      setOpciones(VACIAS());
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
            <input
              id="pregunta"
              value={pregunta}
              maxLength={MAX_LARGO_PREGUNTA}
              placeholder="¿Quién irá primero al terminar el minuto?"
              onChange={(e) => {
                setPregunta(e.target.value);
                setMensaje(null);
              }}
            />
          </div>

          <fieldset className={estilos.opciones}>
            <legend>
              Opciones ({MIN_OPCIONES} a {MAX_OPCIONES})
            </legend>
            {opciones.map((o, i) => (
              <div key={i} className={estilos.opcion}>
                <label htmlFor={`opcion-${i}`} className="solo-lectores">
                  Opción {i + 1}
                </label>
                <input
                  id={`opcion-${i}`}
                  value={o}
                  maxLength={MAX_LARGO_OPCION}
                  placeholder={`Opción ${i + 1}`}
                  onChange={(e) => cambiarOpcion(i, e.target.value)}
                />
                <button
                  type="button"
                  className="boton boton-secundario"
                  aria-label={`Quitar opción ${i + 1}`}
                  disabled={opciones.length <= MIN_OPCIONES}
                  onClick={() => setOpciones((lista) => lista.filter((_, j) => j !== i))}
                >
                  Quitar
                </button>
              </div>
            ))}
            <button
              type="button"
              className={`boton boton-secundario ${estilos.agregar}`}
              disabled={opciones.length >= MAX_OPCIONES}
              onClick={() => setOpciones((lista) => [...lista, ""])}
            >
              Agregar opción
            </button>
          </fieldset>

          <p className="texto-secundario">
            Al abrirlo empieza la ventana de apuestas. El servidor lo cierra solo al terminarla y, tras la ventana de observación, lo
            liquida.
          </p>

          {/* El error de validacion solo se muestra cuando ya se escribio algo. */}
          {!validacion.ok && (pregunta !== "" || opciones.some((o) => o !== "")) && (
            <p className={estilos.aviso}>{validacion.error}</p>
          )}
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
