// Pruebas de HU-03 en el front: saldo y compra de tokens contra Pagos (con
// fetch simulado). Cada `it` corresponde a un criterio de la historia.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WsProvider } from "../api/WsContexto";
import { ClienteWs, type SocketLike } from "../api/ws";
import App from "../App";
import { AuthProvider, USUARIOS_SIMULADOS, type Usuario } from "../auth/AuthContext";
import { validarMonto } from "./api";

// ---------------------------------------------------------------------------
// Pagos simulado: responde segun "METODO /ruta". Una ruta puede tener una
// lista de respuestas, que se entregan en orden (para simular reintentos).
// ---------------------------------------------------------------------------
type Respuesta = { status: number; body?: unknown } | Error | Promise<{ status: number; body?: unknown }>;
let rutas: Record<string, Respuesta | Respuesta[]>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  rutas = {};
  fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const clave = `${init?.method ?? "GET"} ${url.replace(/^\/api\/pagos/, "")}`;
    const definida = rutas[clave];
    let r = (Array.isArray(definida) ? definida.shift() : definida) ?? { status: 404, body: {} };
    r = await r;
    if (r instanceof Error) throw r;
    const { status, body } = r;
    return { ok: status >= 200 && status < 300, status, json: async () => body ?? null };
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

const socketMudo = (): SocketLike => ({ onopen: null, onmessage: null, onclose: null, onerror: null, close() {} });

function abrir(ruta = "/billetera", usuario: Usuario = USUARIOS_SIMULADOS.player) {
  render(
    <MemoryRouter initialEntries={[ruta]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider usuarioInicial={usuario}>
        <WsProvider cliente={new ClienteWs({ url: "ws://prueba/ws", crearSocket: socketMudo })}>
          <App />
        </WsProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

const BILLETERA = { status: 200, body: { saldo: 2450, cop_por_token: 1000 } };

function compra(saldo: number, tokens: number) {
  return { status: 201, body: { compra_id: 1, tokens_acreditados: tokens, saldo, creada_en: "2026-10-10T20:00:00Z" } };
}

function compras() {
  return fetchMock.mock.calls.filter(([url, init]) => url === "/api/pagos/billetera/compras" && init?.method === "POST");
}

const clavesEnviadas = () => compras().map(([, init]) => (init.headers as Record<string, string>)["Idempotency-Key"]);
const botonComprar = () => screen.getByRole("button", { name: /Comprar tokens|Comprando/ });
const saldoEnHeader = () => screen.getByRole("link", { name: /^Saldo:/ });

describe("HU-03 · ver el saldo", () => {
  it("muestra el saldo de Pagos en la pantalla y en el header", async () => {
    rutas = { "GET /billetera": BILLETERA };
    abrir();

    expect(await screen.findByTestId("saldo")).toHaveTextContent("2.450 PBT");
    expect(saldoEnHeader()).toHaveTextContent("2.450 PBT");
    expect(screen.getByText(/1 PBT = \$1\.000 COP/)).toBeInTheDocument();
  });

  it("si el saldo no carga, lo explica y permite reintentar", async () => {
    rutas = { "GET /billetera": [new TypeError("Failed to fetch"), BILLETERA] };
    abrir();

    expect(await screen.findByRole("alert")).toHaveTextContent("No se pudo conectar con el servidor");
    fireEvent.click(screen.getByRole("button", { name: "Reintentar" }));
    expect(await screen.findByTestId("saldo")).toHaveTextContent("2.450 PBT");
  });

  it("el admin no tiene billetera: no se le pide el saldo a Pagos", async () => {
    abrir("/admin", USUARIOS_SIMULADOS.admin);

    await screen.findByRole("heading", { name: "Panel de administración" });
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/billetera"))).toBe(false);
  });
});

describe("HU-03 · comprar tokens", () => {
  it("convierte con la tasa, envia la compra y muestra el saldo nuevo en todos lados", async () => {
    rutas = { "GET /billetera": BILLETERA, "POST /billetera/compras": compra(2500, 50) };
    abrir();
    await screen.findByText("50 PBT"); // "Recibes": $50.000 / 1.000

    fireEvent.click(botonComprar());

    expect(await screen.findByText("Compra aprobada: recibiste 50 PBT.")).toBeInTheDocument();
    expect(screen.getByTestId("saldo")).toHaveTextContent("2.500 PBT");
    expect(saldoEnHeader()).toHaveTextContent("2.500 PBT");
    const [[, init]] = compras();
    expect(JSON.parse(init.body)).toEqual({ monto_cop: 50000 });
    expect(init.credentials).toBe("include");
    expect(clavesEnviadas()[0]).toBeTruthy();
  });

  it("un monto vacio, en 0 o que no es multiplo de la tasa no se puede enviar", async () => {
    rutas = { "GET /billetera": BILLETERA };
    abrir();
    const monto = await screen.findByLabelText("Monto en pesos (COP)");
    await screen.findByText("50 PBT");

    fireEvent.change(monto, { target: { value: "" } });
    expect(botonComprar()).toBeDisabled();

    fireEvent.change(monto, { target: { value: "0" } });
    expect(botonComprar()).toBeDisabled();

    fireEvent.change(monto, { target: { value: "1500" } });
    expect(screen.getByText("El monto debe ser múltiplo de $1.000 COP")).toBeInTheDocument();
    expect(botonComprar()).toBeDisabled();

    fireEvent.submit(botonComprar().closest("form")!);
    expect(compras()).toHaveLength(0);
  });

  it("doble clic: el boton se bloquea y se envia una sola compra", async () => {
    let responder!: (r: { status: number; body?: unknown }) => void;
    rutas = {
      "GET /billetera": BILLETERA,
      "POST /billetera/compras": new Promise((r) => (responder = r)),
    };
    abrir();
    await screen.findByText("50 PBT");

    fireEvent.click(botonComprar());
    fireEvent.click(botonComprar());

    expect(botonComprar()).toBeDisabled();
    expect(botonComprar()).toHaveTextContent("Comprando…");
    responder(compra(2500, 50));
    await screen.findByText("Compra aprobada: recibiste 50 PBT.");
    expect(compras()).toHaveLength(1);
  });

  it("si falla la red, el reintento reusa la misma clave; la compra siguiente lleva otra", async () => {
    rutas = {
      "GET /billetera": BILLETERA,
      "POST /billetera/compras": [new TypeError("Failed to fetch"), compra(2500, 50), compra(2520, 20)],
    };
    abrir();
    await screen.findByText("50 PBT");

    fireEvent.click(botonComprar());
    expect(await screen.findByRole("alert")).toHaveTextContent("No se pudo conectar con el servidor");

    fireEvent.click(botonComprar()); // reintento del mismo pedido
    await screen.findByText("Compra aprobada: recibiste 50 PBT.");

    fireEvent.click(screen.getByRole("button", { name: "$20.000" }));
    fireEvent.click(botonComprar()); // compra nueva
    await screen.findByText("Compra aprobada: recibiste 20 PBT.");

    const [primera, reintento, nueva] = clavesEnviadas();
    expect(reintento).toBe(primera); // Pagos no acredita dos veces (RNF-02)
    expect(nueva).not.toBe(primera);
  });

  it("si Pagos rechaza la compra, muestra su mensaje y el saldo no cambia", async () => {
    rutas = {
      "GET /billetera": BILLETERA,
      "POST /billetera/compras": { status: 422, body: { detail: "El monto máximo por compra es $10.000.000 COP" } },
    };
    abrir();
    await screen.findByText("50 PBT");

    fireEvent.click(botonComprar());

    expect(await screen.findByRole("alert")).toHaveTextContent("El monto máximo por compra es $10.000.000 COP");
    await waitFor(() => expect(botonComprar()).toBeEnabled());
    expect(screen.getByTestId("saldo")).toHaveTextContent("2.450 PBT");
  });
});

describe("validarMonto", () => {
  it.each([
    [50000, 1000, null],
    [0, 1000, "El monto debe ser mayor que 0"],
    [-1000, 1000, "El monto debe ser mayor que 0"],
    [1500, 1000, "El monto debe ser múltiplo de $1.000 COP"],
    [10_001_000, 1000, "El monto máximo por compra es $10.000.000 COP"],
  ])("monto %s con tasa %s -> %s", (monto, tasa, esperado) => {
    expect(validarMonto(monto, tasa)).toBe(esperado);
  });
});
