# HU-12 — Aprovisionamiento inicial del administrador

## Alcance implementado en esta fase

Pagos valida que `ADMIN_EMAIL` y `ADMIN_PASSWORD` estén configurados y no sean
blancos. Durante el ciclo de vida de FastAPI, después de esperar a PostgreSQL y
crear las tablas, aprovisiona el administrador en PostgreSQL.

Si no existe una cuenta con ese correo, crea un usuario con rol `admin` y guarda
la contraseña únicamente mediante el hash bcrypt de `hash_password()`. Si ya
existe con rol `admin`, el arranque no crea un duplicado ni modifica sus datos.
Si el correo pertenece a un usuario con otro rol, el aprovisionamiento falla y
no cambia la cuenta existente. Un conflicto de unicidad concurrente se acepta
solo si, tras revertir la transacción fallida, se confirma que otra instancia
creó el mismo administrador.

La validación de credenciales no aplica reglas de formato de correo ni de
complejidad de contraseña. `ADMIN_NAME` conserva su valor predeterminado.

La recuperación de concurrencia solo reconoce una violación PostgreSQL con
SQLSTATE `23505`, tabla `users` y nombre de constraint que el inspector de
SQLAlchemy confirme como una unicidad sobre la columna `email`. Si el dialecto,
el driver o el catálogo no permiten confirmar esa identidad, el error original
se propaga. La prueba automatizada simula esos diagnósticos; no sustituye una
prueba de concurrencia real contra PostgreSQL.

## Configuración

Completar `ADMIN_EMAIL` y `ADMIN_PASSWORD` en el entorno local antes de iniciar
el servicio. `.env.example` deja ambos valores vacíos deliberadamente. No se
deben guardar credenciales reales en el repositorio.

## Límites y criterios pendientes

Esta fase no implementa registro público, login, compra de tokens, panel del
administrador ni apertura de mercados. Los criterios de HU-12 que dependen de
HU-01, HU-02, HU-03 y HU-11 continúan pendientes de integración. La autorización
de apuestas para administradores ya se aplica en el Motor; probarla con la
cuenta aprovisionada depende del login integrado.

## Pruebas

Las pruebas específicas están en `services/pagos/tests/test_admin_bootstrap.py`.
Incluyen creación, repetición, hash, configuración ausente o vacía, conflicto
con un usuario existente y tratamiento de una colisión de unicidad simulada.

## Validación ejecutada

- `python -m pytest -q tests/test_admin_bootstrap.py`: 12 pasaron.
- `python -m compileall -q app tests`: completado correctamente.
- `python -m pytest -q -rs`: 23 pasaron y 3 se omitieron porque PostgreSQL no
  estaba disponible. Los tres skips corresponden a pruebas de `test_api.py`;
  no validan el aprovisionamiento en PostgreSQL.
- `git diff --check`: completado sin errores.

Las pruebas focalizadas usan SQLite en memoria y una simulación de colisión de
unicidad; no sustituyen una prueba concurrente real ni una validación del
ciclo de vida contra PostgreSQL.
