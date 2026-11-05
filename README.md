# CV Keyword Tailor

App web local, escrita en Go, que adapta tu CV a cada oferta laboral **sin inventar nada**.

0. Entrás con un nombre de usuario temporal y **tu propia API key** (BYOK): Gemini, OpenAI, Anthropic o Groq.
1. Importás tu CV en PDF una sola vez (la IA lo convierte en un JSON editable).
2. Pegás el texto de una oferta: la IA saca el puesto, la empresa y las keywords, y las compara con tu CV.
3. La IA propone intercambios puntuales de palabras (por ejemplo `Postgres` → `PostgreSQL`).
4. Go valida cada propuesta, vos elegís cuáles aplicar y descargás el PDF en formato Harvard.

La IA propone, Go valida y vos decidís. **Cero dependencias externas**: solo la librería estándar.

## Requisitos

- Go 1.22 o superior.
- Google Chrome o Chromium instalado (para imprimir el PDF).
- Una API key de alguno de los proveedores soportados (se carga desde la app, no por variable de entorno).

## Cómo correrla

```bash
go run ./cmd/server
# abrí http://127.0.0.1:8080
```

## Cuenta y API key (BYOK)

Al entrar, la app pide un **nombre de usuario temporal** y tu **API key**. El proveedor se detecta por las primeras letras de la clave:

| Prefijo | Proveedor | Importa PDF |
| --- | --- | --- |
| `AQ` (o `AIza`) | Gemini | sí |
| `sk-ant-` | Anthropic | sí |
| `sk-` | OpenAI | sí |
| `gsk` | Groq | no: completá el CV a mano o cargá el ejemplo |

La clave se **verifica** contra el proveedor listando sus modelos: si la rechaza, no se entra. Después elegís el modelo, que se puede cambiar en cualquier momento desde la barra superior. En **Cuenta** podés cambiar el nombre, pegar otra clave (incluso de otro proveedor) o cerrar sesión.

La clave vive **solo en la memoria del servidor**: la cookie lleva un token al azar, nunca la clave. Se borra al cerrar sesión o al reiniciar la app (y hay que volver a entrar).

| Variable | Para qué | Por defecto |
| --- | --- | --- |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GEMINI_BASE_URL`, `GROQ_BASE_URL` | URL base de cada API (proxy o pruebas) | la oficial |
| `ADDR` / `PORT` | Dónde escucha | `127.0.0.1:8080` |
| `DATA_DIR` | Dónde se guardan los JSON | `data` |
| `CHROME_PATH` | Ruta a Chrome si no se autodetecta | autodetecta |
| `CHROME_NO_SANDBOX` | `1` para pasar `--no-sandbox` (Docker). Como root se agrega solo | (vacío) |
| `CV_LANG` | Idioma de los títulos del CV: `es` o `en` | `es` |

El usuario es temporal y **el CV maestro y las versiones son compartidos** entre sesiones: es una app personal, dejala escuchando solo en `127.0.0.1`. Rechaza además los POST que vengan de otros sitios.

## Qué se guarda

Todo en `DATA_DIR` (ya está en `.gitignore`, son datos personales):

- `cv.json`: el CV maestro. Antes de reemplazarlo (al importar otro PDF o cargar el ejemplo) queda una copia en `cv.backup.json`.
- `versions/<id>.json`: una versión por oferta, con la oferta, las keywords, los cambios (aplicados o no) y la foto del CV resultante. Se puede volver a descargar aunque después edites el maestro.

## La garantía anti-invención

Cada cambio propuesto pasa por `cv.ValidateChanges` y se descarta, con el motivo a la vista, si:

- el `target_id` no existe en el CV;
- el texto `before` no aparece **literal** en ese elemento;
- no trae `keyword`, o `before` y `after` están vacíos o son iguales;
- `after` agrega dígitos que no estaban en `before`;
- `after` agrega nombres propios (palabras en mayúscula) que no estaban en `before` ni son la keyword.

La validación se **repite al crear la versión**: nada de lo que vuelve del formulario se aplica sin pasar de nuevo por Go. Las keywords `missing` nunca se agregan; solo se informan.

La regla de nombres propios es una heurística conservadora: ante la duda descarta, así que puede rechazar algún cambio legítimo. Queda informado y lo podés aplicar a mano en el CV maestro.

## Estructura

```text
cmd/server/         arranque y configuración por entorno
internal/cv/        modelo del CV, IDs, validador y aplicador de cambios
internal/ai/        interfaz Analyzer, detección de proveedor, clientes de Gemini/OpenAI/Anthropic/Groq y prompts
internal/render/    plantilla Harvard (HTML) y PDF con Chrome headless
internal/store/     lectura y escritura atómica de los JSON
internal/web/       handlers, sesiones BYOK, plantillas de pantalla y seguridad
```

## Tests

```bash
go vet ./...
go test ./...
```

Los tests de IA corren contra un servidor simulado (no gastan llamadas). `TestChromePDF` usa Chrome de verdad y se saltea si no encuentra uno; con `CHROME_PATH` podés elegir cuál. Con `CVTAILOR_DUMP_PDF=ruta.pdf` guarda el PDF generado para mirarlo.

## Límites conocidos

- El texto que la IA no pudo ubicar (`unparsed`) se muestra en la pantalla de revisión, pero **no se imprime en el PDF** hasta que lo pases a una sección.
- Los IDs de bullets (`exp-1-b2`) se regeneran por posición cada vez que guardás el CV maestro.
- Los intercambios se aplican sobre el resumen, los bullets de experiencia y proyectos, y los items de skills (`Postgres` → `PostgreSQL`). Los detalles de educación no se pueden intercambiar.
- Fuera de la v1: varias plantillas, edición directa sobre la vista previa, ofertas por URL, deploy.
