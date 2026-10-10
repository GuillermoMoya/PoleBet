// Pruebas de ws.ts con un WebSocket falso y el reloj controlado.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ClienteWs, type EstadoWs, type SocketLike } from "./ws";

// WebSocket falso: guarda cada instancia creada y deja simular sus eventos.
class SocketFalso implements SocketLike {
  static creados: SocketFalso[] = [];
  onopen: SocketLike["onopen"] = null;
  onmessage: SocketLike["onmessage"] = null;
  onclose: SocketLike["onclose"] = null;
  onerror: SocketLike["onerror"] = null;
  cerrado = false;

  constructor(public url: string) {
    SocketFalso.creados.push(this);
  }

  close() {
    this.cerrado = true;
  }

  // Helpers para simular lo que haria el servidor.
  abrir() {
    this.onopen?.({});
  }
  caer() {
    this.onclose?.({});
  }
  recibir(mensaje: unknown) {
    this.onmessage?.({ data: typeof mensaje === "string" ? mensaje : JSON.stringify(mensaje) });
  }
}

// Crea un cliente que usa sockets falsos.
function nuevoCliente() {
  return new ClienteWs({ url: "ws://prueba/ws", crearSocket: (url) => new SocketFalso(url) });
}

// Devuelve el ultimo socket que creo el cliente.
function ultimo(): SocketFalso {
  return SocketFalso.creados[SocketFalso.creados.length - 1];
}

beforeEach(() => {
  vi.useFakeTimers();
  SocketFalso.creados = [];
});

afterEach(() => vi.useRealTimers());

describe("suscripcion", () => {
  it("entrega el payload solo a los manejadores de ese tipo", () => {
    const cliente = nuevoCliente();
    const posiciones = vi.fn();
    const mercado = vi.fn();
    cliente.suscribir("posiciones", posiciones);
    cliente.suscribir("mercado_abierto", mercado);
    cliente.conectar();
    ultimo().abrir();

    ultimo().recibir({ type: "posiciones", payload: { vuelta: 3 } });

    expect(posiciones).toHaveBeenCalledWith({ vuelta: 3 });
    expect(mercado).not.toHaveBeenCalled();
  });

  it("deja de entregar tras desuscribirse, sin afectar a los demas", () => {
    const cliente = nuevoCliente();
    const uno = vi.fn();
    const dos = vi.fn();
    const quitarUno = cliente.suscribir("posiciones", uno);
    cliente.suscribir("posiciones", dos);
    cliente.conectar();
    ultimo().abrir();

    ultimo().recibir({ type: "posiciones", payload: 1 });
    quitarUno();
    quitarUno(); // llamarla dos veces no debe romper nada
    ultimo().recibir({ type: "posiciones", payload: 2 });

    expect(uno).toHaveBeenCalledTimes(1);
    expect(dos).toHaveBeenCalledTimes(2);
  });

  it("ignora mensajes que no son un sobre valido", () => {
    const cliente = nuevoCliente();
    const manejador = vi.fn();
    cliente.suscribir("posiciones", manejador);
    cliente.conectar();
    ultimo().abrir();

    ultimo().recibir("esto no es json");
    ultimo().recibir("null");
    ultimo().recibir({ payload: "sin tipo" });

    expect(manejador).not.toHaveBeenCalled();
  });

  it("conserva las suscripciones despues de reconectar", () => {
    const cliente = nuevoCliente();
    const manejador = vi.fn();
    cliente.suscribir("posiciones", manejador);
    cliente.conectar();
    ultimo().abrir();
    ultimo().caer();
    vi.advanceTimersByTime(1000);
    ultimo().abrir();

    ultimo().recibir({ type: "posiciones", payload: "tras reconectar" });

    expect(manejador).toHaveBeenCalledWith("tras reconectar");
  });
});

describe("estado y reconexion", () => {
  it("pasa por conectando -> abierta -> reconectando -> abierta", () => {
    const cliente = nuevoCliente();
    const estados: EstadoWs[] = [];
    cliente.alCambiarEstado((e) => estados.push(e));

    expect(cliente.estado).toBe("cerrada");
    cliente.conectar();
    ultimo().abrir();
    ultimo().caer();
    vi.advanceTimersByTime(1000);
    ultimo().abrir();

    expect(estados).toEqual(["conectando", "abierta", "reconectando", "abierta"]);
    expect(SocketFalso.creados).toHaveLength(2);
  });

  it("reintenta con espera creciente: 1, 2, 4, 8 y luego cada 10 s", () => {
    const cliente = nuevoCliente();
    cliente.conectar();

    // Cada intento falla sin llegar a abrir.
    for (const espera of [1000, 2000, 4000, 8000, 10000, 10000]) {
      const antes = SocketFalso.creados.length;
      ultimo().caer();
      vi.advanceTimersByTime(espera - 1);
      expect(SocketFalso.creados).toHaveLength(antes); // todavia no
      vi.advanceTimersByTime(1);
      expect(SocketFalso.creados).toHaveLength(antes + 1); // ahora si
      expect(cliente.estado).toBe("reconectando");
    }
  });

  it("vuelve a la espera de 1 s despues de una conexion exitosa", () => {
    const cliente = nuevoCliente();
    cliente.conectar();
    ultimo().caer();
    vi.advanceTimersByTime(1000);
    ultimo().caer();
    vi.advanceTimersByTime(2000);
    ultimo().abrir(); // el tercer intento si abre
    const antes = SocketFalso.creados.length;

    ultimo().caer();
    vi.advanceTimersByTime(1000);

    expect(SocketFalso.creados).toHaveLength(antes + 1);
  });

  it("cerrar() detiene la reconexion y cierra el socket", () => {
    const cliente = nuevoCliente();
    cliente.conectar();
    ultimo().abrir();
    const socket = ultimo();

    cliente.cerrar();
    socket.caer(); // el cierre del socket descartado no debe reconectar
    vi.advanceTimersByTime(60_000);

    expect(socket.cerrado).toBe(true);
    expect(cliente.estado).toBe("cerrada");
    expect(SocketFalso.creados).toHaveLength(1);
  });

  it("cerrar() cancela un reintento que ya estaba programado", () => {
    const cliente = nuevoCliente();
    cliente.conectar();
    ultimo().caer(); // programa el reintento a 1 s

    cliente.cerrar();
    vi.advanceTimersByTime(60_000);

    expect(SocketFalso.creados).toHaveLength(1);
  });

  it("conectar() dos veces no abre dos conexiones", () => {
    const cliente = nuevoCliente();
    cliente.conectar();
    cliente.conectar();
    expect(SocketFalso.creados).toHaveLength(1);
  });
});
