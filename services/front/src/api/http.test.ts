// Pruebas de http.ts: como arma la peticion y como traduce los errores.
import { afterEach, describe, expect, it, vi } from "vitest";
import { configurarHttp, ErrorHttp, http, pedir } from "./http";

// Crea un fetch falso que responde con el status y el cuerpo dados.
function fetchQueResponde(status: number, cuerpo?: unknown) {
  return vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => {
      if (cuerpo === undefined) throw new Error("sin cuerpo");
      return cuerpo;
    },
  })) as unknown as typeof fetch;
}

// Ejecuta la peticion y devuelve el error que lanza.
async function errorDe(promesa: Promise<unknown>): Promise<ErrorHttp> {
  try {
    await promesa;
  } catch (error) {
    return error as ErrorHttp;
  }
  throw new Error("se esperaba un error");
}

afterEach(() => configurarHttp({}));

describe("pedir", () => {
  it("antepone la URL base del servicio y envia la cookie de sesion", async () => {
    const fetchFn = fetchQueResponde(200, { ok: true });
    const datos = await http.get<{ ok: boolean }>("motor", "/posiciones", { fetchFn });

    expect(datos).toEqual({ ok: true });
    expect(fetchFn).toHaveBeenCalledWith(
      "/api/motor/posiciones",
      expect.objectContaining({ method: "GET", credentials: "include" }),
    );
  });

  it("envia el cuerpo como JSON en un POST", async () => {
    const fetchFn = fetchQueResponde(201, { id: 1 });
    await http.post("pagos", "/compras", { monto: 5 }, { fetchFn });

    const [url, init] = vi.mocked(fetchFn).mock.calls[0];
    expect(url).toBe("/api/pagos/compras");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify({ monto: 5 }));
    expect((init?.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("acepta una respuesta sin cuerpo (204)", async () => {
    await expect(pedir("motor", "/algo", { fetchFn: fetchQueResponde(204) })).resolves.toBeNull();
  });

  it("ante un 401 limpia la sesion y lanza el error normalizado", async () => {
    const sesionVencida = vi.fn();
    configurarHttp({ sesionVencida });

    const error = await errorDe(pedir("motor", "/posiciones", { fetchFn: fetchQueResponde(401, { error: "no autorizado" }) }));

    expect(sesionVencida).toHaveBeenCalledTimes(1);
    expect(error).toBeInstanceOf(ErrorHttp);
    expect(error.status).toBe(401);
    expect(error.message).toMatch(/sesión/i);
  });

  it("ante un 403 avisa de acceso denegado sin limpiar la sesion", async () => {
    const sesionVencida = vi.fn();
    const accesoDenegado = vi.fn();
    configurarHttp({ sesionVencida, accesoDenegado });

    const error = await errorDe(pedir("motor", "/mercados", { fetchFn: fetchQueResponde(403, { error: "x" }) }));

    expect(error.status).toBe(403);
    expect(accesoDenegado).toHaveBeenCalledWith(error.message);
    expect(sesionVencida).not.toHaveBeenCalled();
  });

  it("usa el mensaje del motor ({ error }) y el de FastAPI ({ detail })", async () => {
    const delMotor = await errorDe(pedir("motor", "/apuestas", { fetchFn: fetchQueResponde(409, { error: "Mercado cerrado" }) }));
    expect([delMotor.status, delMotor.message]).toEqual([409, "Mercado cerrado"]);

    const dePagos = await errorDe(pedir("pagos", "/compras", { fetchFn: fetchQueResponde(422, { detail: "Monto invalido" }) }));
    expect([dePagos.status, dePagos.message]).toEqual([422, "Monto invalido"]);
  });

  it("pone un mensaje generico si el error no trae uno legible", async () => {
    const error = await errorDe(pedir("motor", "/x", { fetchFn: fetchQueResponde(500) }));
    expect(error.status).toBe(500);
    expect(error.message).toMatch(/inesperado/i);
  });

  it("convierte un fallo de red en status 0", async () => {
    const fetchFn = vi.fn(async () => {
      throw new TypeError("fallo de red");
    }) as unknown as typeof fetch;

    const error = await errorDe(pedir("motor", "/posiciones", { fetchFn }));
    expect(error.status).toBe(0);
    expect(error.message).toMatch(/conectar/i);
  });
});
