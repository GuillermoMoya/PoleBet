// Pruebas de HU-02: sesion real contra Pagos (con fetch simulado).
//
// Cada `it` corresponde a un criterio de aceptacion de la historia.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WsProvider } from "../api/WsContexto";
import { ClienteWs, type SocketLike } from "../api/ws";
import App from "../App";
import { AuthProvider, USUARIOS_SIMULADOS, type Usuario } from "./AuthContext";
import { destinoTrasLogin } from "./destino";

const JUGADOR: Usuario = { id: "11111111-1111-4111-8111-111111111111", nombre: "Ana Pérez", email: "ana@arqui.local", rol: "player" };
const ADMIN: Usuario = { ...USUARIOS_SIMULADOS.admin };

// ---------------------------------------------------------------------------
// Pagos simulado: responde segun "METODO /ruta". Lo que no esta definido
// responde 404 (p. ej. las posiciones que pide /carrera al montar).
// ---------------------------------------------------------------------------
type Respuesta = { status: number; body?: unknown } | Error;
let rutas: Record<string, Respuesta>;
let fetchMock: ReturnType<typeof vi.fn>;

function pagos(definidas: Record<string, Respuesta>) {
  rutas = definidas;
}

beforeEach(() => {
  rutas = {};
  fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const ruta = url.replace(/^\/api\/pagos/, "");
    const r = rutas[`${init?.method ?? "GET"} ${ruta}`] ?? { status: 404, body: {} };
    if (r instanceof Error) throw r;
    return { ok: r.status >= 200 && r.status < 300, status: r.status, json: async () => r.body ?? null };
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

const socketMudo = (): SocketLike => ({ onopen: null, onmessage: null, onclose: null, onerror: null, close() {} });

// Monta la app como en produccion: SIN usuarioInicial, asi AuthProvider
// pregunta a Pagos por la sesion (GET /auth/me).
function abrir(ruta: string, state?: unknown) {
  render(
    <MemoryRouter initialEntries={[{ pathname: ruta, state }]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider>
        <WsProvider cliente={new ClienteWs({ url: "ws://prueba/ws", crearSocket: socketMudo })}>
          <App />
        </WsProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

// Espera a que AuthProvider termine de preguntar por la sesion (el formulario
// aparece despues del "Cargando…") y envia las credenciales.
async function llenarYEnviar(email: string, password: string) {
  fireEvent.change(await screen.findByLabelText("Correo"), { target: { value: email } });
  fireEvent.change(screen.getByLabelText("Contraseña"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));
}

function llamadas(metodo: string, ruta: string) {
  return fetchMock.mock.calls.filter(([url, init]) => url === `/api/pagos${ruta}` && (init?.method ?? "GET") === metodo);
}

describe("HU-02 · iniciar sesion", () => {
  it("con credenciales correctas entra a la app y la cookie la maneja el navegador", async () => {
    pagos({ "GET /auth/me": { status: 401, body: { detail: "No hay sesion" } }, "POST /auth/login": { status: 200, body: JUGADOR } });
    abrir("/login");

    await llenarYEnviar("  ana@arqui.local ", "clave123");

    expect(await screen.findByRole("heading", { name: "Carrera en vivo" })).toBeInTheDocument();
    const [[, init]] = llamadas("POST", "/auth/login");
    expect(JSON.parse(init.body)).toEqual({ email: "ana@arqui.local", password: "clave123" });
    expect(init.credentials).toBe("include"); // sin esto el navegador ignora la cookie
    expect(screen.getByTitle("Ana Pérez")).toHaveTextContent("AP"); // header con el usuario real
  });

  it("con credenciales incorrectas muestra el mensaje generico y limpia la contraseña", async () => {
    pagos({ "GET /auth/me": { status: 401 }, "POST /auth/login": { status: 401, body: { detail: "Correo o contraseña incorrectos" } } });
    abrir("/login");

    await llenarYEnviar("ana@arqui.local", "mala");

    expect(await screen.findByRole("alert")).toHaveTextContent("Correo o contraseña incorrectos");
    expect(screen.getByLabelText("Contraseña")).toHaveValue("");
    expect(screen.getByRole("heading", { name: "Iniciar sesión" })).toBeInTheDocument();
  });

  it("valida los campos vacios sin llamar a Pagos", async () => {
    pagos({ "GET /auth/me": { status: 401 } });
    abrir("/login");
    await screen.findByLabelText("Correo");

    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    expect(screen.getByText("Escribe tu correo.")).toBeInTheDocument();
    expect(screen.getByText("Escribe tu contraseña.")).toBeInTheDocument();
    expect(llamadas("POST", "/auth/login")).toHaveLength(0);
  });

  it("si Pagos no responde, explica que no hay conexion", async () => {
    pagos({ "GET /auth/me": { status: 401 }, "POST /auth/login": new TypeError("Failed to fetch") });
    abrir("/login");

    await llenarYEnviar("ana@arqui.local", "clave123");

    expect(await screen.findByRole("alert")).toHaveTextContent("No se pudo conectar con el servidor");
  });

  it("el admin entra a su panel aunque viniera de una ruta de jugador", async () => {
    pagos({ "GET /auth/me": { status: 401 }, "POST /auth/login": { status: 200, body: ADMIN } });
    abrir("/login", { desde: "/carrera" });

    await llenarYEnviar("admin@demo.local", "admin1234");

    expect(await screen.findByRole("link", { name: "Admin" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Acceso denegado" })).not.toBeInTheDocument();
  });
});

describe("HU-02 · sesion existente y vencida", () => {
  it("al recargar, recupera la sesion con /auth/me sin pasar por el login", async () => {
    pagos({ "GET /auth/me": { status: 200, body: JUGADOR } });
    abrir("/billetera");

    expect(screen.getByRole("status")).toHaveTextContent("Cargando"); // no redirige mientras pregunta
    expect(await screen.findByText("Pendiente: HU-03")).toBeInTheDocument();
  });

  it("sin sesion (401), una ruta protegida lleva al login", async () => {
    pagos({ "GET /auth/me": { status: 401 } });
    abrir("/carrera");

    expect(await screen.findByRole("heading", { name: "Iniciar sesión" })).toBeInTheDocument();
  });

  it("con sesion abierta, /login lleva al inicio", async () => {
    pagos({ "GET /auth/me": { status: 200, body: JUGADOR } });
    abrir("/login");

    expect(await screen.findByRole("heading", { name: "Carrera en vivo" })).toBeInTheDocument();
  });
});

describe("HU-02 · cerrar sesion", () => {
  it("borra la cookie en Pagos y vuelve a la pantalla de ingreso", async () => {
    pagos({ "GET /auth/me": { status: 200, body: JUGADOR }, "POST /auth/logout": { status: 204 } });
    abrir("/billetera");
    await screen.findByText("Pendiente: HU-03");

    fireEvent.click(screen.getByRole("button", { name: "Salir" }));

    expect(await screen.findByRole("heading", { name: "Iniciar sesión" })).toBeInTheDocument();
    expect(llamadas("POST", "/auth/logout")).toHaveLength(1);
  });

  it("si Pagos no responde al salir, igual cierra la sesion en el front", async () => {
    pagos({ "GET /auth/me": { status: 200, body: JUGADOR }, "POST /auth/logout": new TypeError("Failed to fetch") });
    abrir("/billetera");
    await screen.findByText("Pendiente: HU-03");

    fireEvent.click(screen.getByRole("button", { name: "Salir" }));

    await waitFor(() => expect(screen.getByRole("heading", { name: "Iniciar sesión" })).toBeInTheDocument());
  });
});

describe("destinoTrasLogin", () => {
  it.each([
    ["player", undefined, "/carrera"],
    ["admin", undefined, "/admin"],
    ["player", "/billetera", "/billetera"],
    ["player", "/admin", "/carrera"],
    ["admin", "/carrera", "/admin"],
    ["admin", "/estado", "/estado"],
    ["player", "//sitio-malicioso.com", "/carrera"],
    ["player", "https://sitio-malicioso.com", "/carrera"],
  ] as const)("rol %s desde %s -> %s", (rol, desde, esperado) => {
    expect(destinoTrasLogin(rol, desde)).toBe(esperado);
  });
});
