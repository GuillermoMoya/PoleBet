// Estado del mercado (HU-11): etiqueta de la fase, cuenta regresiva y la
// linea de tiempo Abierto → Cerrado (en observacion) → Liquidado. Lo usan la
// pantalla del jugador (/carrera) y el panel del admin (/admin).

import type { Mercado } from "../api/tipos";
import { faseDe, formatoReloj, segundosHasta, TEXTO_FASE, type FaseMercado } from "./mercado";
import estilos from "./EstadoMercado.module.css";

interface Props {
  mercado: Mercado;
  ahoraServidor: number; // ms, de useRelojServidor
  mostrarOpciones?: boolean;
  mostrarPregunta?: boolean; // false si al lado ya va el formulario de apuesta, que la muestra
  mostrarCuenta?: boolean; // idem: el formulario trae su propia cuenta regresiva
}

const PASOS: { fases: FaseMercado[]; texto: string }[] = [
  { fases: ["abierto"], texto: "Abierto" },
  { fases: ["observacion", "liquidando"], texto: "Cerrado (en observación)" },
  { fases: ["liquidado"], texto: "Liquidado" },
];

function cuenta(m: Mercado, fase: FaseMercado, ahora: number): string {
  switch (fase) {
    case "abierto":
      return `Cierra en ${formatoReloj(segundosHasta(m.cierra_en, ahora))}`;
    case "observacion":
      return `Se liquida en ${formatoReloj(segundosHasta(m.observa_hasta, ahora))}`;
    case "liquidando":
      return "Esperando el resultado…";
    case "liquidado":
      return "Resultado listo";
  }
}

export default function EstadoMercado({
  mercado,
  ahoraServidor,
  mostrarOpciones = false,
  mostrarPregunta = true,
  mostrarCuenta = true,
}: Props) {
  const fase = faseDe(mercado, ahoraServidor);
  const indice = PASOS.findIndex((p) => p.fases.includes(fase));

  return (
    <div className={estilos.estado}>
      <div className={estilos.cabecera}>
        {/* Solo la fase se anuncia a lectores de pantalla; la cuenta cambia
            cada segundo y seria ruido. */}
        <span className={`etiqueta ${fase === "abierto" ? "" : estilos.etiquetaApagada}`} aria-live="polite" data-testid="fase-mercado">
          {TEXTO_FASE[fase]}
        </span>
        {mostrarCuenta && (
          <span className={estilos.cuenta} data-testid="cuenta-mercado">
            {cuenta(mercado, fase, ahoraServidor)}
          </span>
        )}
      </div>

      {mostrarPregunta && <h3 className={estilos.pregunta}>{mercado.pregunta}</h3>}

      {mostrarOpciones && (
        <ul className={estilos.opciones} aria-label="Opciones del mercado">
          {mercado.opciones.map((o) => (
            <li key={o}>{o}</li>
          ))}
        </ul>
      )}

      <ol className={estilos.pasos} aria-label="Ciclo del mercado">
        {PASOS.map((p, i) => (
          <li
            key={p.texto}
            className={i < indice ? estilos.pasoHecho : i === indice ? estilos.pasoActual : undefined}
            aria-current={i === indice ? "step" : undefined}
          >
            {p.texto}
          </li>
        ))}
      </ol>
    </div>
  );
}
