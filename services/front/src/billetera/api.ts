// Llamadas REST de la billetera contra Pagos (HU-03).
//
//   GET  /billetera          -> { saldo, cop_por_token }
//   POST /billetera/compras  { monto_cop } + header Idempotency-Key
//                            -> 201 compra nueva | 200 reenvio de la misma clave
//
// Pagos rechaza con 422 (monto invalido), 409 (clave usada con otro monto) y
// 403 (el admin no compra). http.ts trae el mensaje en español en ErrorHttp.

import { http } from "../api/http";

export interface Billetera {
  saldo: number; // tokens disponibles
  cop_por_token: number; // tasa fija de conversion
}

export interface Compra {
  compra_id: number; // id del movimiento en el ledger
  tokens_acreditados: number;
  saldo: number; // saldo despues de la compra
  creada_en: string; // ISO
}

// Tope por compra, el mismo de Pagos (MONTO_MAXIMO_COP en services/billetera.py).
export const MONTO_MAXIMO_COP = 10_000_000;

export function obtenerBilletera(signal?: AbortSignal): Promise<Billetera> {
  return http.get<Billetera>("pagos", "/billetera", { signal });
}

// `clave` identifica el intento de compra: si se reenvia la misma (reintento
// tras un fallo de red), Pagos no acredita dos veces (RNF-02).
export function comprarTokens(montoCop: number, clave: string): Promise<Compra> {
  return http.post<Compra>("pagos", "/billetera/compras", { monto_cop: montoCop }, { headers: { "Idempotency-Key": clave } });
}

const numero = new Intl.NumberFormat("es-CO");

// Mismas reglas que Pagos, para avisar antes de enviar. Devuelve el mensaje
// de error o null si el monto es valido. Pagos vuelve a validar de todas formas.
export function validarMonto(montoCop: number, copPorToken: number): string | null {
  if (montoCop <= 0) return "El monto debe ser mayor que 0";
  if (montoCop > MONTO_MAXIMO_COP) return `El monto máximo por compra es $${numero.format(MONTO_MAXIMO_COP)} COP`;
  if (montoCop % copPorToken !== 0) return `El monto debe ser múltiplo de $${numero.format(copPorToken)} COP`;
  return null;
}
