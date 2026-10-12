// Pruebas de HU-11 en el front: la logica del ciclo del mercado, el panel del
// admin (/admin) y el mercado en la pantalla del jugador (/carrera), con REST
// y WebSocket falsos. Cada `it` de pantalla corresponde a un criterio.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mercado } from "../api/tipos";
import { WsProvider } from "../api/WsContexto";
import { ClienteWs, type SocketLike } from "../api/ws";
import App from "../App";
import { AuthProvider, USUARIOS_SIMULADOS, type Usuario } from "../auth/AuthContext";
import { desfaseDe, esMercado, faseDe, formatoReloj, reemplaza, segundosHasta, validarPeticion } from "./mercado";

// ---------------------------------------------------------------------------
// Datos
// ---------------------------------------------------------------------------

const T0 = Date.parse("2026-10-11T15:00:00.000Z");
const iso = (ms: number) => new Date(ms).toISOString();

// Mercado abierto en `abre` (ms) con ventana de 30 s y observacion de 60 s.
function mercado(abre = T0, cambios: Partial<Mercado> = {}): Mercado {
  return {
    id: `m-${abre}`,
    carrera_id: "2024_monza",
    pregunta: "¿Quién irá primero al terminar el minuto?",
    opciones: ["VER", "NOR", "LEC"],
    estado: "abierto",
    abre_en: iso(abre),
    cierra_en: iso(abre + 30_000),
    observa_hasta: iso(abre + 90_000),
    ahora: iso(abre),
    ...cambios,
  };
}

// ---------------------------------------------------------------------------
// Logica
// ---------------------------------------------------------------------------

describe("logica del mercado", () => {
  it("esMercado acepta un mercado y rechaza lo demas", () => {
    expect(esMercado(mercado())).toBe(true);
    expect(esMercado(null)).toBe(false);
    expect(esMercado({ ...mercado(), estado: "pausado" })).toBe(false);
    expect(esMercado({ ...mercado(), opciones: [1, 2] })).toBe(false);
  });

  it("faseDe sigue el reloj del servidor: abierto → observacion → liquidando → liquidado", () => {
    const m = mercado();
    expect(faseDe(m, T0 + 29_999)).toBe("abierto");
    expect(faseDe(m, T0 + 30_000)).toBe("observacion"); // aun sin el mensaje mercado_cerrado
    expect(faseDe({ ...m, estado: "cerrado" }, T0 + 60_000)).toBe("observacion");
    expect(faseDe({ ...m, estado: "cerrado" }, T0 + 90_000)).toBe("liquidando");
    expect(faseDe({ ...m, estado: "liquidado" }, T0)).toBe("liquidado");
  });

  it("desfaseDe mide cuanto va adelantado el servidor", () => {
    expect(desfaseDe({ ahora: iso(T0 + 5_000) }, T0)).toBe(5_000);
    expect(desfaseDe({ ahora: "basura" }, T0)).toBe(0);
  });

  it("segundosHasta y formatoReloj", () => {
    expect(segundosHasta(iso(T0 + 30_000), T0)).toBe(30);
    expect(segundosHasta(iso(T0 + 29_001), T0)).toBe(30); // redondea hacia arriba
    expect(segundosHasta(iso(T0), T0 + 1)).toBe(0);
    expect(formatoReloj(75)).toBe("1:15");
    expect(formatoReloj(5)).toBe("0:05");
  });

  it("reemplaza: el mismo mercado o uno mas nuevo, nunca uno viejo", () => {
    const viejo = mercado(T0);
    const nuevo = mercado(T0 + 120_000);
    expect(reemplaza(viejo, null)).toBe(true);
    expect(reemplaza({ ...viejo, estado: "cerrado" }, viejo)).toBe(true);
    expect(reemplaza(nuevo, viejo)).toBe(true);
    expect(reemplaza(viejo, nuevo)).toBe(false);
  });

  it.each([
    ["  ", ["A", "B"], "Escribe la pregunta"],
    ["x".repeat(201), ["A", "B"], "más de 200"],
    ["¿?", ["A"], "entre 2 y 10"],
    ["¿?", Array.from({ length: 11 }, (_, i) => `o${i}`), "entre 2 y 10"],
    ["¿?", ["A", " "], "vacía"],
    ["¿?", ["A", "x".repeat(101)], "máximo 100"],
    ["¿?", ["VER", "ver "], "repetida"],
  ])("validarPeticion(%j, %j) rechaza con %j", (pregunta, opciones, error) => {
    const r = validarPeticion(pregunta, opciones);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toContain(error);
  });

  it("validarPeticion limpia espacios", () => {
    expect(validarPeticion("  ¿Quién?  ", [" VER ", "NOR"])).toEqual({
      ok: true,
      peticion: { pregunta: "¿Quién?", opciones: ["VER", "NOR"] },
    });
  });
});

// ---------------------------------------------------------------------------
// Pantallas, con REST y WebSocket falsos
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

// El motor envia un mensaje por el WebSocket.
function enviarWs(type: string, payload: unknown) {
  act(() => SocketFalso.ultimo.onmessage?.({ data: JSON.stringify({ type, payload }) }));
}

// Backends simulados: responden segun "METODO /ruta" (sin el prefijo /api/...).
type Respuesta = { status: number; body?: unknown };
let rutas: Record<string, Respuesta>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  rutas = {};
  fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const clave = `${init?.method ?? "GET"} ${url.replace(/^\/api\/(pagos|motor)/, "")}`;
    const { status, body } = rutas[clave] ?? { status: 404, body: {} };
    return { ok: status >= 200 && status < 300, status, json: async () => body ?? null };
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

function abrir(ruta: string, usuario: Usuario) {
  const cliente = new ClienteWs({ url: "ws://prueba/ws", crearSocket: () => new SocketFalso(), esperasMs: [10] });
  render(
    <MemoryRouter initialEntries={[ruta]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider usuarioInicial={usuario}>
        <WsProvider cliente={cliente}>
          <App />
        </WsProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

const llamadas = (metodo: string, ruta: string) =>
  fetchMock.mock.calls.filter(([url, init]) => url === `/api/motor${ruta}` && (init?.method ?? "GET") === metodo);

// Un mercado "de ahora" para las pantallas (usan el reloj real).
const mercadoAhora = (cambios: Partial<Mercado> = {}) => mercado(Date.now(), cambios);

describe("HU-11 · panel del administrador", () => {
  const botonAbrir = () => screen.getByRole("button", { name: /Abrir mercado|Abriendo/ });

  function llenar(pregunta: string, opciones: string[]) {
    fireEvent.change(screen.getByLabelText("Pregunta"), { target: { value: pregunta } });
    opciones.forEach((o, i) => {
      if (i >= 2) fireEvent.click(screen.getByRole("button", { name: "Agregar opción" }));
      fireEvent.change(screen.getByLabelText(`Opción ${i + 1}`), { target: { value: o } });
    });
  }

  it("sin mercados, lo dice", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: null } };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    expect(await screen.findByText("Todavía no se ha abierto ningún mercado.")).toBeInTheDocument();
  });

  it("abre un mercado con pregunta y opciones y lo muestra abierto con su cuenta regresiva", async () => {
    const creado = mercadoAhora({ pregunta: "¿Quién gana?", opciones: ["VER", "NOR", "LEC"] });
    rutas = { "GET /mercados/actual": { status: 200, body: null }, "POST /mercados": { status: 201, body: creado } };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    await screen.findByText("Todavía no se ha abierto ningún mercado.");

    llenar("  ¿Quién gana?  ", ["VER", "NOR", "LEC"]);
    fireEvent.click(botonAbrir());

    expect(await screen.findByRole("status")).toHaveTextContent("Mercado abierto");
    const [[, init]] = llamadas("POST", "/mercados");
    expect(JSON.parse(init.body)).toEqual({ pregunta: "¿Quién gana?", opciones: ["VER", "NOR", "LEC"] });
    expect(init.credentials).toBe("include"); // viaja la cookie de sesion con el rol admin
    expect(screen.getByTestId("fase-mercado")).toHaveTextContent("Abierto");
    expect(screen.getByTestId("cuenta-mercado")).toHaveTextContent(/Cierra en 0:(30|29)/);
    // El formulario se limpia y no deja abrir otro mientras este siga abierto.
    expect(screen.getByLabelText("Pregunta")).toHaveValue("");
    expect(screen.getByText(/Ya hay un mercado abierto/)).toBeInTheDocument();
    expect(botonAbrir()).toBeDisabled();
  });

  it("no deja enviar un mercado invalido y explica por que", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: null } };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    await screen.findByText("Todavía no se ha abierto ningún mercado.");
    expect(botonAbrir()).toBeDisabled();

    llenar("¿Quién gana?", ["VER", "ver"]);
    expect(screen.getByText('La opción "ver" está repetida')).toBeInTheDocument();
    expect(botonAbrir()).toBeDisabled();

    fireEvent.submit(botonAbrir().closest("form")!);
    expect(llamadas("POST", "/mercados")).toHaveLength(0);
  });

  it("permite entre 2 y 10 opciones", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: null } };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    await screen.findByText("Todavía no se ha abierto ningún mercado.");

    expect(screen.getByRole("button", { name: "Quitar opción 1" })).toBeDisabled();
    const agregar = screen.getByRole("button", { name: "Agregar opción" });
    for (let i = 0; i < 8; i++) fireEvent.click(agregar);
    expect(screen.getByLabelText("Opción 10")).toBeInTheDocument();
    expect(agregar).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Quitar opción 10" }));
    expect(screen.queryByLabelText("Opción 10")).not.toBeInTheDocument();
    expect(agregar).toBeEnabled();
  });

  it("si el motor lo rechaza, muestra su mensaje", async () => {
    rutas = {
      "GET /mercados/actual": { status: 200, body: null },
      "POST /mercados": { status: 409, body: { error: "Ya hay un mercado abierto; espera a que cierre para abrir otro" } },
    };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    await screen.findByText("Todavía no se ha abierto ningún mercado.");

    llenar("¿Quién gana?", ["VER", "NOR"]);
    fireEvent.click(botonAbrir());

    expect(await screen.findByRole("alert")).toHaveTextContent("Ya hay un mercado abierto; espera a que cierre");
    expect(screen.getByLabelText("Pregunta")).toHaveValue("¿Quién gana?"); // no se pierde lo escrito
  });

  it("ve como el servidor lo cierra: Abierto → Cerrado (en observacion)", async () => {
    const m = mercadoAhora();
    rutas = { "GET /mercados/actual": { status: 200, body: m } };
    abrir("/admin", USUARIOS_SIMULADOS.admin);
    expect(await screen.findByTestId("fase-mercado")).toHaveTextContent("Abierto");

    enviarWs("mercado_cerrado", { ...m, estado: "cerrado", ahora: iso(Date.now()) });

    expect(screen.getByTestId("fase-mercado")).toHaveTextContent("Cerrado · en observación");
    expect(screen.getByRole("button", { name: "Abrir mercado" })).toBeDisabled(); // falta llenar el formulario
    expect(screen.queryByText(/Ya hay un mercado abierto/)).not.toBeInTheDocument();
  });
});

describe("HU-11 · el jugador ve el mercado en vivo", () => {
  it("sin mercado, espera al administrador", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: null } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    expect(await screen.findByText(/No hay ningún mercado abierto/)).toBeInTheDocument();
  });

  it("cuando el admin lo abre, aparece al instante por WebSocket con opciones y cuenta regresiva", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: null } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    await screen.findByText(/No hay ningún mercado abierto/);

    enviarWs("mercado_abierto", mercadoAhora());

    expect(screen.getByText("¿Quién irá primero al terminar el minuto?")).toBeInTheDocument();
    expect(screen.getByTestId("fase-mercado")).toHaveTextContent("Abierto");
    expect(screen.getByRole("radio", { name: "NOR" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Apostar" })).toBeInTheDocument();
    expect(screen.getByText(/Cierra en 0:(30|29)/)).toBeInTheDocument();
  });

  it("al entrar con un mercado ya abierto, lo ve sin esperar un mensaje", async () => {
    rutas = { "GET /mercados/actual": { status: 200, body: mercadoAhora() } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    expect(await screen.findByRole("button", { name: "Apostar" })).toBeInTheDocument();
  });

  it("la cuenta regresiva usa el reloj del servidor aunque el del navegador este corrido", async () => {
    // El servidor va 60 s adelante del navegador.
    const servidor = Date.now() + 60_000;
    rutas = { "GET /mercados/actual": { status: 200, body: mercado(servidor) } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    // Sin el desfase mostraria 1:30.
    expect(await screen.findByText(/Cierra en 0:(30|29)/)).toBeInTheDocument();
  });

  it("al cerrar, desaparece el formulario y pasa a «Cerrado · en observación»", async () => {
    const m = mercadoAhora();
    rutas = { "GET /mercados/actual": { status: 200, body: m } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    await screen.findByRole("button", { name: "Apostar" });

    enviarWs("mercado_cerrado", { ...m, estado: "cerrado", ahora: iso(Date.now()) });

    expect(screen.queryByRole("button", { name: "Apostar" })).not.toBeInTheDocument();
    expect(screen.getByTestId("fase-mercado")).toHaveTextContent("Cerrado · en observación");
    expect(screen.getByTestId("cuenta-mercado")).toHaveTextContent(/Se liquida en 1:(30|29)/);
    expect(screen.getByRole("list", { name: "Opciones del mercado" })).toHaveTextContent("VERNORLEC");
  });

  it("si la ventana ya paso segun el servidor, lo muestra cerrado aunque aun no llegue el mensaje", async () => {
    const abre = Date.now() - 31_000;
    rutas = { "GET /mercados/actual": { status: 200, body: mercado(abre, { ahora: iso(Date.now()) }) } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    expect(await screen.findByTestId("fase-mercado")).toHaveTextContent("Cerrado · en observación");
    expect(screen.queryByRole("button", { name: "Apostar" })).not.toBeInTheDocument();
  });

  it("al liquidarse (HU-07) muestra «Liquidado», aunque el mensaje solo traiga el id", async () => {
    const m = mercadoAhora({ estado: "cerrado" });
    rutas = { "GET /mercados/actual": { status: 200, body: m } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    await screen.findByTestId("fase-mercado");

    enviarWs("mercado_liquidado", { mercado_id: m.id });

    expect(screen.getByTestId("fase-mercado")).toHaveTextContent("Liquidado");
    expect(screen.getByRole("listitem", { current: "step" })).toHaveTextContent("Liquidado");
  });

  it("un mensaje de un mercado viejo no reemplaza al actual", async () => {
    const actual = mercadoAhora({ pregunta: "Pregunta nueva" });
    rutas = { "GET /mercados/actual": { status: 200, body: actual } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    await screen.findByText("Pregunta nueva");

    enviarWs("mercado_cerrado", mercado(Date.now() - 600_000, { pregunta: "Pregunta vieja", estado: "cerrado" }));

    expect(screen.getByText("Pregunta nueva")).toBeInTheDocument();
    expect(screen.queryByText("Pregunta vieja")).not.toBeInTheDocument();
  });

  it("si el mercado no carga, permite reintentar", async () => {
    rutas = { "GET /mercados/actual": { status: 500, body: { error: "Error interno" } } };
    abrir("/carrera", USUARIOS_SIMULADOS.player);
    expect(await screen.findByText("Error interno")).toBeInTheDocument();

    rutas["GET /mercados/actual"] = { status: 200, body: mercadoAhora() };
    fireEvent.click(screen.getByRole("button", { name: "Reintentar" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Apostar" })).toBeInTheDocument());
  });
});
