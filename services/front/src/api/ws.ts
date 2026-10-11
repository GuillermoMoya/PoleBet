// Cliente del WebSocket del motor, sin React para poder probarlo solo.
//
//   - Una sola conexion para toda la app (la crea WsContexto.tsx).
//   - Suscripcion por tipo de mensaje: suscribir("posiciones", fn).
//   - Si la conexion se cae, se reconecta sola con espera creciente
//     (1 s, 2 s, 4 s, 8 s y luego cada 10 s).
//
// El front no envia nada por el WebSocket: las acciones van por REST.

import type { MensajeWs } from "./tipos";

export type EstadoWs = "conectando" | "abierta" | "reconectando" | "cerrada";

// Esperas entre intentos de reconexion. La ultima se repite.
export const ESPERAS_MS = [1000, 2000, 4000, 8000, 10000];

// Lo minimo que el cliente usa de un WebSocket. Permite pasar uno falso en
// las pruebas.
export interface SocketLike {
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  close(): void;
}

export type Manejador<T = unknown> = (payload: T) => void;
type OyenteEstado = (estado: EstadoWs) => void;

export interface OpcionesWs {
  url: string;
  crearSocket?: (url: string) => SocketLike; // reemplaza a WebSocket en las pruebas
  esperasMs?: number[];
}

export class ClienteWs {
  private readonly url: string;
  private readonly crearSocket: (url: string) => SocketLike;
  private readonly esperasMs: number[];

  private socket: SocketLike | null = null;
  private estadoActual: EstadoWs = "cerrada";
  private activo = false; // true entre conectar() y cerrar()
  private intentos = 0; // reconexiones seguidas sin lograr abrir
  private temporizador: ReturnType<typeof setTimeout> | null = null;

  private readonly manejadores = new Map<string, Set<Manejador>>();
  private readonly oyentesEstado = new Set<OyenteEstado>();

  constructor({ url, crearSocket, esperasMs = ESPERAS_MS }: OpcionesWs) {
    this.url = url;
    this.crearSocket = crearSocket ?? ((u) => new WebSocket(u) as unknown as SocketLike);
    this.esperasMs = esperasMs;
  }

  // Estado actual de la conexion.
  get estado(): EstadoWs {
    return this.estadoActual;
  }

  // Abre la conexion y la mantiene viva hasta que se llame cerrar().
  conectar(): void {
    if (this.activo) return;
    this.activo = true;
    this.intentos = 0;
    this.abrir("conectando");
  }

  // Cierra la conexion y cancela cualquier reconexion pendiente.
  cerrar(): void {
    this.activo = false;
    if (this.temporizador !== null) {
      clearTimeout(this.temporizador);
      this.temporizador = null;
    }
    const socket = this.socket;
    this.socket = null; // asi su onclose ya no dispara una reconexion
    socket?.close();
    this.cambiarEstado("cerrada");
  }

  // Registra un manejador para un tipo de mensaje. Devuelve la funcion que lo
  // quita.
  suscribir<T = unknown>(tipo: string, manejador: Manejador<T>): () => void {
    const grupo = this.manejadores.get(tipo) ?? new Set<Manejador>();
    this.manejadores.set(tipo, grupo);
    grupo.add(manejador as Manejador);
    return () => {
      grupo.delete(manejador as Manejador);
      // Solo se borra el grupo si sigue siendo el vigente para ese tipo.
      if (grupo.size === 0 && this.manejadores.get(tipo) === grupo) this.manejadores.delete(tipo);
    };
  }

  // Avisa cada vez que cambia el estado de la conexion. Devuelve la funcion
  // que quita el aviso.
  alCambiarEstado(oyente: OyenteEstado): () => void {
    this.oyentesEstado.add(oyente);
    return () => {
      this.oyentesEstado.delete(oyente);
    };
  }

  // Crea el socket y conecta sus eventos. Cada evento revisa que el socket
  // siga siendo el vigente, para ignorar los de una conexion ya descartada.
  private abrir(estado: EstadoWs): void {
    this.cambiarEstado(estado);

    let socket: SocketLike;
    try {
      socket = this.crearSocket(this.url);
    } catch {
      this.programarReconexion();
      return;
    }
    this.socket = socket;

    socket.onopen = () => {
      if (this.socket !== socket) return;
      this.intentos = 0;
      this.cambiarEstado("abierta");
    };
    socket.onmessage = (ev) => {
      if (this.socket !== socket) return;
      this.despachar(ev.data);
    };
    socket.onclose = () => {
      if (this.socket !== socket) return;
      this.socket = null;
      this.programarReconexion();
    };
    // Tras un error el navegador siempre dispara onclose; se reconecta alli.
    socket.onerror = () => {};
  }

  // Programa el siguiente intento con la espera que toca.
  private programarReconexion(): void {
    if (!this.activo) return;
    this.cambiarEstado("reconectando");
    const espera = this.esperasMs[Math.min(this.intentos, this.esperasMs.length - 1)];
    this.intentos += 1;
    this.temporizador = setTimeout(() => {
      this.temporizador = null;
      this.abrir("reconectando");
    }, espera);
  }

  // Entrega el mensaje a los manejadores de su tipo. Lo que no sea un sobre
  // { type, payload } valido se ignora.
  private despachar(datos: unknown): void {
    if (typeof datos !== "string") return;
    let mensaje: MensajeWs;
    try {
      mensaje = JSON.parse(datos) as MensajeWs;
    } catch {
      return;
    }
    if (typeof mensaje !== "object" || mensaje === null || typeof mensaje.type !== "string") return;
    // Se copia el grupo por si un manejador se desuscribe mientras se recorre.
    for (const manejador of [...(this.manejadores.get(mensaje.type) ?? [])]) {
      manejador(mensaje.payload);
    }
  }

  // Guarda el estado nuevo y avisa a los oyentes solo si cambio.
  private cambiarEstado(nuevo: EstadoWs): void {
    if (nuevo === this.estadoActual) return;
    this.estadoActual = nuevo;
    for (const oyente of [...this.oyentesEstado]) oyente(nuevo);
  }
}
