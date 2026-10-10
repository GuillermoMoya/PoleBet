// Firma un JWT de desarrollo con el mismo contrato que usara Pagos (HS256,
// claims "sub" y "rol", "exp" obligatorio). Sirve para abrir el WebSocket del
// motor real mientras no exista el login (HU-02).
//
//   npm run jwt                       jugador, 120 minutos
//   npm run jwt -- --rol admin        administrador
//   npm run jwt -- --min 1            vence en 1 minuto (probar el 401)
//   npm run jwt -- --sub <uuid>       otro usuario
//
// El secreto sale de JWT_SECRET o, si no esta, del .env de la raiz del repo.
// SOLO PARA DESARROLLO: nunca usar con el secreto de un entorno real.

import { createHmac, randomUUID } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const RAIZ = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const COOKIE = "session"; // services/motor/internal/auth/contrato.go
const ROLES = ["player", "admin"];

// Lee una opcion "--nombre valor" de la linea de comandos.
function opcion(nombre, porDefecto) {
  const i = process.argv.indexOf(`--${nombre}`);
  return i !== -1 && process.argv[i + 1] ? process.argv[i + 1] : porDefecto;
}

// Busca JWT_SECRET en el entorno y luego en .env y .env.example de la raiz.
function leerSecreto() {
  if (process.env.JWT_SECRET) return { secreto: process.env.JWT_SECRET, origen: "variable de entorno" };
  for (const archivo of [".env", ".env.example"]) {
    const ruta = resolve(RAIZ, archivo);
    if (!existsSync(ruta)) continue;
    const linea = readFileSync(ruta, "utf8").match(/^JWT_SECRET=(.*)$/m);
    if (linea && linea[1].trim()) return { secreto: linea[1].trim(), origen: archivo };
  }
  return null;
}

// Codifica en base64url, como exige el formato JWT.
function b64url(dato) {
  return Buffer.from(dato).toString("base64url");
}

// Arma y firma el token: cabecera.cuerpo.firma
function firmar(claims, secreto) {
  const contenido = `${b64url(JSON.stringify({ alg: "HS256", typ: "JWT" }))}.${b64url(JSON.stringify(claims))}`;
  const firma = createHmac("sha256", secreto).update(contenido).digest("base64url");
  return `${contenido}.${firma}`;
}

// ---- Programa ----
const rol = opcion("rol", "player");
const minutos = Number(opcion("min", "120"));
const sub = opcion("sub", randomUUID());

if (!ROLES.includes(rol)) {
  console.error(`Rol desconocido: "${rol}". Usa ${ROLES.join(" o ")}.`);
  process.exit(1);
}
if (!Number.isFinite(minutos) || minutos <= 0) {
  console.error("--min debe ser un numero de minutos mayor que 0.");
  process.exit(1);
}
const encontrado = leerSecreto();
if (!encontrado) {
  console.error("No se encontro JWT_SECRET. Definelo en el entorno o en el .env de la raiz.");
  process.exit(1);
}

const ahora = Math.floor(Date.now() / 1000);
const token = firmar({ sub, rol, iat: ahora, exp: ahora + Math.round(minutos * 60) }, encontrado.secreto);

console.log(`JWT de desarrollo (rol=${rol}, sub=${sub}, vence en ${minutos} min)`);
console.log(`Secreto tomado de: ${encontrado.origen}\n`);
console.log(token);
console.log("\nPara usarlo en el navegador, abre el front, pega esto en la consola y recarga:\n");
console.log(`  document.cookie = "${COOKIE}=${token}; path=/; max-age=${Math.round(minutos * 60)}";`);
console.log("\nPara probarlo sin navegador (debe responder 101 Switching Protocols):\n");
console.log(
  `  curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGVzdGluZzEyMzQ1Njc4OQ==" -H "Cookie: ${COOKIE}=${token}" http://localhost:8080/ws`,
);
