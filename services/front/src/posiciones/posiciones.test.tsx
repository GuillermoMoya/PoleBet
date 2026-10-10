// Pruebas de HU-05: la logica de las fotos, la tabla y el hook completo
// (carga inicial por REST, mensajes del WebSocket y reconexion).
import { act, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FotoPosiciones, Piloto } from "../api/tipos";
import { WsProvider } from "../api/WsContexto";
import { ClienteWs, type SocketLike } from "../api/ws";
import { AuthProvider, USUARIOS_SIMULADOS } from "../auth/AuthContext";
import AvisoConexion from "./AvisoConexion";
import { detectarCambios, esFoto, esMasReciente, latenciaDe } from "./foto";
import PanelPosiciones from "./PanelPosiciones";
import TablaPosiciones from "./TablaPosiciones";
import { usePosiciones } from "./usePosiciones";

// Crea un piloto de prueba.
function piloto(posicion: number, numero: number, codigo: string): Piloto {
  return { posicion, numero, codigo, nombre: `Piloto ${codigo}`, equipo: "Equipo", diferencia: posicion === 1 ? "Líder" : `+${posicion}.0` };
}

// Crea una foto con los pilotos en el orden dado.
function foto(codigos: [number, string][], actualizado_en = "2026-10-04T18:30:00.000Z"): FotoPosiciones {
  return { carrera_id: "prueba", vuelta: 18, actualizado_en, pilotos: codigos.map(([numero, codigo], i) => piloto(i + 1, numero, codigo)) };
}

const INICIAL = foto([[4, "VAR"], [16, "OKA"], [55, "SAN"]]);
const CON_CAMBIO = foto([[16, "OKA"], [4, "VAR"], [55, "SAN"]], "2026-10-04T18:30:02.000Z");

describe("logica de las fotos", () => {
  it("esFoto acepta una foto y rechaza lo demas", () => {
    expect(esFoto(INICIAL)).toBe(true);
    expect(esFoto(null)).toBe(false);
    expect(esFoto({ pilotos: "no es lista" })).toBe(false);
    expect(esFoto({ pilotos: [{ posicion: "1" }] })).toBe(false);
  });

  it("detectarCambios dice quien subio y quien bajo", () => {
    const cambios = detectarCambios(INICIAL, CON_CAMBIO);
    expect(cambios.get(16)).toBe("sube");
    expect(cambios.get(4)).toBe("baja");
    expect(cambios.has(55)).toBe(false);
    expect(detectarCambios(null, INICIAL).size).toBe(0); // primera carga
  });

  it("esMasReciente descarta una foto mas vieja", () => {
    expect(esMasReciente(CON_CAMBIO, INICIAL)).toBe(true);
    expect(esMasReciente(INICIAL, CON_CAMBIO)).toBe(false);
    expect(esMasReciente({ ...INICIAL, actualizado_en: "basura" }, CON_CAMBIO)).toBe(true);
  });

  it("latenciaDe mide desde emitido_en", () => {
    const ahora = Date.parse("2026-10-04T18:30:00.250Z");
    expect(latenciaDe({ ...INICIAL, emitido_en: "2026-10-04T18:30:00.000Z" }, ahora)).toBe(250);
    expect(latenciaDe(INICIAL, ahora)).toBeNull();
  });
});

describe("TablaPosiciones", () => {
  it("muestra posicion, numero, codigo, nombre, equipo y diferencia", () => {
    render(<TablaPosiciones pilotos={INICIAL.pilotos} />);
    const filas = screen.getAllByRole("row").slice(1); // sin el encabezado

    expect(filas).toHaveLength(3);
    const lider = within(filas[0]);
    expect(lider.getByText("1")).toBeInTheDocument();
    expect(lider.getByText("4")).toBeInTheDocument();
    expect(lider.getByText("VAR")).toBeInTheDocument();
    expect(lider.getByText("Piloto VAR")).toBeInTheDocument();
    expect(lider.getByText("Equipo")).toBeInTheDocument();
    expect(lider.getByText("Líder")).toBeInTheDocument();
  });

  it("resalta la fila del piloto que cambio de posicion", () => {
    render(<TablaPosiciones pilotos={CON_CAMBIO.pilotos} cambios={detectarCambios(INICIAL, CON_CAMBIO)} />);
    const filas = screen.getAllByRole("row").slice(1);

    expect(filas[0]).toHaveAttribute("data-movimiento", "sube");
    expect(filas[1]).toHaveAttribute("data-movimiento", "baja");
    expect(filas[2]).not.toHaveAttribute("data-movimiento");
  });
});

describe("AvisoConexion", () => {
  it("avisa mientras reconecta y calla con la conexion abierta", () => {
    const { rerender } = render(<AvisoConexion estado="reconectando" />);
    expect(screen.getByRole("status")).toHaveTextContent("Reconectando…");

    rerender(<AvisoConexion estado="abierta" />);
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
  });
});

// ---------------------------------------------------------------------------
// Hook completo, con REST y WebSocket falsos
// ---------------------------------------------------------------------------

// WebSocket falso minimo (ver ws.test.ts).
class SocketFalso implements SocketLike {
  static ultimo: SocketFalso;
  onopen: SocketLike["onopen"] = null;
  onmessage: SocketLike["onmessage"] = null;
  onclose: SocketLike["onclose"] = null;
  onerror: SocketLike["onerror"] = null;
  constructor() {
    SocketFalso.ultimo = this;
  }
  close() {}
}

// Pantalla minima que usa el hook como lo hace /carrera.
function Pantalla() {
  const posiciones = usePosiciones();
  return (
    <>
      <PanelPosiciones posiciones={posiciones} />
      <output data-testid="latencia">{posiciones.latenciaMs ?? "sin dato"}</output>
    </>
  );
}

// Monta la pantalla con sesion y un cliente WebSocket falso.
function montar() {
  const cliente = new ClienteWs({ url: "ws://prueba/ws", crearSocket: () => new SocketFalso(), esperasMs: [10] });
  render(
    <AuthProvider usuarioInicial={USUARIOS_SIMULADOS.player}>
      <WsProvider cliente={cliente}>
        <Pantalla />
      </WsProvider>
    </AuthProvider>,
  );
}

// Simula que el motor envia un mensaje por el WebSocket.
function enviarWs(type: string, payload: unknown) {
  act(() => SocketFalso.ultimo.onmessage?.({ data: JSON.stringify({ type, payload }) }));
}

// Lee los codigos de los pilotos en el orden en que estan en pantalla.
function codigosEnPantalla(): string[] {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((fila) => fila.querySelector("b")?.textContent ?? "");
}

// Hace que GET /posiciones responda con el status y el cuerpo dados.
function restResponde(status: number, cuerpo: unknown) {
  vi.mocked(fetch).mockResolvedValue({ ok: status < 300, status, json: async () => cuerpo } as Response);
}

describe("usePosiciones", () => {
  beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
  afterEach(() => vi.unstubAllGlobals());

  it("muestra las posiciones al entrar, sin oprimir nada (criterio 1)", async () => {
    restResponde(200, INICIAL);
    montar();

    expect(screen.getByText("Cargando posiciones…")).toBeInTheDocument();
    await waitFor(() => expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]));
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/motor/posiciones");
  });

  it("actualiza la tabla con cada mensaje del WebSocket y mide la latencia (criterio 2)", async () => {
    restResponde(200, INICIAL);
    montar();
    await waitFor(() => expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]));

    enviarWs("posiciones", { ...CON_CAMBIO, emitido_en: new Date().toISOString() });

    expect(codigosEnPantalla()).toEqual(["OKA", "VAR", "SAN"]);
    expect(screen.getAllByRole("row")[1]).toHaveAttribute("data-movimiento", "sube");
    expect(Number(screen.getByTestId("latencia").textContent)).toBeLessThan(1000);
  });

  it("ignora mensajes de otro tipo o mal formados", async () => {
    restResponde(200, INICIAL);
    montar();
    await waitFor(() => expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]));

    enviarWs("mercado_abierto", CON_CAMBIO);
    enviarWs("posiciones", { pilotos: "roto" });

    expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]);
  });

  it("no deja que una foto vieja pise una mas nueva", async () => {
    restResponde(200, CON_CAMBIO);
    montar();
    await waitFor(() => expect(codigosEnPantalla()).toEqual(["OKA", "VAR", "SAN"]));

    enviarWs("posiciones", INICIAL); // mas vieja que la que ya esta

    expect(codigosEnPantalla()).toEqual(["OKA", "VAR", "SAN"]);
  });

  it("al reconectar vuelve a pedir las posiciones actuales (criterio 3)", async () => {
    restResponde(200, INICIAL);
    montar();
    act(() => SocketFalso.ultimo.onopen?.({}));
    await waitFor(() => expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]));
    expect(fetch).toHaveBeenCalledTimes(1);

    // Se cae la conexion; mientras tanto la carrera avanzo.
    restResponde(200, CON_CAMBIO);
    act(() => SocketFalso.ultimo.onclose?.({}));
    // El cliente crea otro socket a los 10 ms; cuando abre, se pide la foto.
    const caido = SocketFalso.ultimo;
    await waitFor(() => expect(SocketFalso.ultimo).not.toBe(caido));
    act(() => SocketFalso.ultimo.onopen?.({}));

    await waitFor(() => expect(codigosEnPantalla()).toEqual(["OKA", "VAR", "SAN"]));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("muestra el estado vacio si no hay carrera en curso", async () => {
    restResponde(404, { error: "no hay carrera" });
    montar();
    expect(await screen.findByText("No hay una carrera en curso.")).toBeInTheDocument();
  });

  it("muestra el error y permite reintentar", async () => {
    vi.mocked(fetch).mockRejectedValue(new TypeError("fallo de red"));
    montar();
    expect(await screen.findByRole("alert")).toHaveTextContent(/No se pudo conectar/);

    restResponde(200, INICIAL);
    act(() => screen.getByRole("button", { name: "Reintentar" }).click());

    await waitFor(() => expect(codigosEnPantalla()).toEqual(["VAR", "OKA", "SAN"]));
  });
});
