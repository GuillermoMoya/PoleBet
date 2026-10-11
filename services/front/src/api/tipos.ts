// Tipos de los mensajes y respuestas que el front intercambia con los backends.
// Si un contrato cambia, se cambia solo aqui.

// ---------------------------------------------------------------------------
// WebSocket del motor
// ---------------------------------------------------------------------------

// Sobre comun de todo lo que el motor envia por WebSocket
// (services/motor/internal/ws/mensajes.go).
export interface MensajeWs<T = unknown> {
  type: string;
  payload: T;
}

// Tipos de mensaje acordados con el motor. Solo "posiciones" esta implementado
// en el front (HU-05); los demas quedan reservados para sus historias.
export const TIPOS_WS = {
  posiciones: "posiciones", // HU-05
  mercadoAbierto: "mercado_abierto", // HU-11
  mercadoCerrado: "mercado_cerrado", // HU-11
  mercadoLiquidado: "mercado_liquidado", // HU-07
  resultadoApuesta: "resultado_apuesta", // HU-08
} as const;

// ---------------------------------------------------------------------------
// HU-05: posiciones en vivo
// ---------------------------------------------------------------------------
// PENDIENTE: formato propuesto. El esquema final lo define HU-04 (datos de
// carrera en MongoDB); cuando se cierre, se ajusta aqui y en scripts/mock-ws.mjs.

export interface Piloto {
  posicion: number;
  numero: number; // numero del carro; identifica al piloto entre fotos
  codigo: string; // tres letras, p. ej. "VAR"
  nombre: string;
  equipo: string;
  color_equipo?: string; // color hex del equipo (opcional)
  diferencia: string; // con el lider: "Lider", "+2.4"
}

// Foto de las posiciones en un instante. Es el payload del mensaje
// "posiciones" y tambien la respuesta de GET /posiciones.
export interface FotoPosiciones {
  carrera_id: string;
  vuelta?: number;
  actualizado_en: string; // ISO: cuando cambio el dato en la carrera
  emitido_en?: string; // ISO: cuando el motor envio el mensaje (latencia, RNF-04)
  pilotos: Piloto[];
}

// ---------------------------------------------------------------------------
// Errores
// ---------------------------------------------------------------------------

// Forma unica de los errores de http.ts. status 0 = no hubo respuesta.
export interface ErrorApi {
  status: number;
  message: string;
}
