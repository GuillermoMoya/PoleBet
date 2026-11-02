// Pruebas de HU-08 en el front: la logica del resultado y el panel
// "Resultado de tu apuesta", con el WebSocket y Pagos falsos.
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ResultadoApuesta } from "../api/tipos";
import { WsProvider } from "../api/WsContexto";
import { ClienteWs, type SocketLike } from "../api/ws";
import { AuthProvider } from "../auth/AuthContext";
import { USUARIOS_SIMULADOS } from "../auth/usuariosPrueba";
import { BilleteraProvider } from "../billetera/BilleteraContexto";
import PanelResultado from "./PanelResultado";
import { alLiquidarse, esResultadoApuesta, ESPERANDO, formatoPuntos } from "./resultado";

// Gano "Sí" apostando 5 tokens: 10 + 5 x 0,5 = 12,5 puntos.
function resultado(cambios: Partial<ResultadoApuesta> = {}): ResultadoApuesta {
  return {
    mercado_id: "m-1",
    apuesta_id: "a-1",
    opcion: "Sí",
    opcion_ganadora: "Sí",
    gano: true,
    puntos_obtenidos: 12.5,
    puntaje_total: 132.5,
    ...cambios,
  };
}

// ---------------------------------------------------------------------------
// Logica
// ---------------------------------------------------------------------------

describe("logica del resultado", () => {
  it("esResultadoApuesta acepta el payload del motor y rechaza lo demas", () => {
    expect(esResultadoApuesta(resultado())).toBe(true);
    expect(esResultadoApuesta(null)).toBe(false);
    expect(esResultadoApuesta({ ...resultado(), gano: "si" })).toBe(false);
    expect(esResultadoApuesta({ ...resultado(), puntos_obtenidos: "12.5" })).toBe(false);
  });

  it("formatoPuntos muestra un decimal sin redondear", () => {
    expect(formatoPuntos(12.5)).toBe("12,5");
    expect(formatoPuntos(60)).toBe("60,0");
    expect(formatoPuntos(0)).toBe("0,0");
  });

  it("alLiquidarse conserva el resultado de ese mercado; si no hay, el jugador no aposto", () => {
    const conResultado = { tipo: "resultado" as const, resultado: resultado() };
    expect(alLiquidarse(conResultado, "m-1")).toBe(conResultado);
    expect(alLiquidarse(conResultado, "m-2")).toEqual({ tipo: "sin_apuesta" });
    expect(alLiquidarse(ESPERANDO, "m-1")).toEqual({ tipo: "sin_apuesta" });
  });
});

// ---------------------------------------------------------------------------
// Panel, con WebSocket y Pagos falsos
// ---------------------------------------------------------------------------

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

// Deja terminar las promesas pendientes (p. ej. la carga del saldo) dentro
// de act, para que React no avise de actualizaciones fuera de la prueba.
const asentar = () => act(() => new Promise<void>((r) => setTimeout(r, 0)));

// El motor envia un mensaje por el WebSocket.
async function enviarWs(type: string, payload: unknown) {
  act(() => SocketFalso.ultimo.onmessage?.({ data: JSON.stringify({ type, payload }) }));
  await asentar();
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  // Solo responde GET /api/billetera (la carga del saldo).
  fetchMock = vi.fn(async (url: string) =>
    url === "/api/billetera"
      ? { ok: true, status: 200, json: async () => ({ saldo: 400, cop_por_token: 100 }) }
      : { ok: false, status: 404, json: async () => ({}) },
  );
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

const cargasDeSaldo = () => fetchMock.mock.calls.filter(([url]) => url === "/api/billetera").length;

async function abrir() {
  const cliente = new ClienteWs({ url: "ws://prueba/ws", crearSocket: () => new SocketFalso(), esperasMs: [10] });
  render(
    <AuthProvider usuarioInicial={USUARIOS_SIMULADOS.player}>
      <WsProvider cliente={cliente}>
        <BilleteraProvider>
          <PanelResultado />
        </BilleteraProvider>
      </WsProvider>
    </AuthProvider>,
  );
  await asentar();
}

const ESPERA = /Cuando se liquide el mercado/;

// El valor de un dato (<dt>/<dd>) del panel.
const dato = (nombre: string) => screen.getByText(nombre).nextElementSibling?.textContent;

describe("HU-08 · resultado de tu apuesta", () => {
  it("antes de liquidarse, espera", async () => {
    await abrir();
    expect(screen.getByText(ESPERA)).toBeInTheDocument();
  });

  it("si gana: ganaste, su opcion y la ganadora, puntos con decimal, puntaje total y recarga el saldo", async () => {
    await abrir();
    expect(cargasDeSaldo()).toBe(1);

    await enviarWs("resultado_apuesta", resultado());

    expect(screen.getByText("¡Ganaste!")).toBeInTheDocument();
    expect(dato("Tu opción")).toBe("Sí");
    expect(dato("Opción ganadora")).toBe("Sí");
    expect(dato("Puntos obtenidos")).toBe("+12,5");
    expect(dato("Puntaje total")).toBe("132,5");
    expect(cargasDeSaldo()).toBe(2);
  });

  it("si pierde: perdiste, 0 puntos y el puntaje no cambia", async () => {
    await abrir();
    await enviarWs("resultado_apuesta", resultado({ opcion: "No", gano: false, puntos_obtenidos: 0, puntaje_total: 120 }));

    expect(screen.getByText("Perdiste")).toBeInTheDocument();
    expect(dato("Tu opción")).toBe("No");
    expect(dato("Opción ganadora")).toBe("Sí");
    expect(dato("Puntos obtenidos")).toBe("0,0");
    expect(dato("Puntaje total")).toBe("120,0");
  });

  it("mercado_liquidado despues del resultado no lo borra", async () => {
    await abrir();
    await enviarWs("resultado_apuesta", resultado());
    await enviarWs("mercado_liquidado", { mercado_id: "m-1", resultado: "Sí", puntos_otorgados: 12.5 });
    expect(screen.getByText("¡Ganaste!")).toBeInTheDocument();
  });

  it("mercado_liquidado sin resultado antes: no aposto", async () => {
    await abrir();
    await enviarWs("mercado_liquidado", { mercado_id: "m-1", resultado: "Sí", puntos_otorgados: 0 });
    expect(screen.getByText("No apostaste en este mercado.")).toBeInTheDocument();
  });

  it("un mercado_abierto nuevo limpia el resultado", async () => {
    await abrir();
    await enviarWs("resultado_apuesta", resultado());
    expect(screen.getByText("¡Ganaste!")).toBeInTheDocument();

    await enviarWs("mercado_abierto", { id: "m-2" });
    expect(screen.queryByText("¡Ganaste!")).not.toBeInTheDocument();
    expect(screen.getByText(ESPERA)).toBeInTheDocument();
  });

  it("ignora un payload invalido", async () => {
    await abrir();
    await enviarWs("resultado_apuesta", { mercado_id: "m-1" });
    expect(screen.getByText(ESPERA)).toBeInTheDocument();
  });
});
