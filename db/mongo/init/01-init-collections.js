// ============================================================
// 01-init-collections.js
// Inicialización de la base de datos MongoDB para el sistema
//
// Este script corre UNA SOLA VEZ, cuando el contenedor de Mongo
// arranca por primera vez. Docker lo ejecuta automáticamente
// porque está montado en /docker-entrypoint-initdb.d
// ============================================================

db = db.getSiblingDB('apuestas_db');

print('⏳ Inicializando base de datos: apuestas_db');

// ============================================================
// 1. COLECCIÓN: carreras
// Metadata de cada carrera (histórica o en vivo) que el motor
// va a reproducir o consumir desde OpenF1.
// ============================================================
db.createCollection('carreras', {
  validator: {
    $jsonSchema: {
      bsonType: 'object',
      required: ['carrera_id', 'nombre', 'fuente', 'creada_en'],
      properties: {
        carrera_id: {
          bsonType: 'string',
          description: 'Identificador único de la carrera (ej: 2024_monza)'
        },
        nombre: {
          bsonType: 'string',
          description: 'Nombre legible de la carrera'
        },
        temporada: {
          bsonType: 'int',
          description: 'Año de la temporada'
        },
        circuito: {
          bsonType: 'string',
          description: 'Nombre del circuito'
        },
        fuente: {
          enum: ['openf1', 'historica', 'simulada'],
          description: 'Origen de los datos'
        },
        estado: {
          enum: ['cargada', 'en_vivo', 'finalizada'],
          description: 'Estado actual de la carrera'
        },
        creada_en: {
          bsonType: 'date',
          description: 'Fecha de carga en el sistema'
        }
      }
    }
  }
});

db.carreras.createIndex({ carrera_id: 1 }, { unique: true });
db.carreras.createIndex({ estado: 1 });

print('✅ Colección "carreras" creada');


// ============================================================
// 2. COLECCIÓN: eventos_carrera
// Datos crudos normalizados de OpenF1 (o de la reproducción
// histórica). Esquema variable porque cada tipo de evento
// tiene campos distintos. Esto justifica el uso de MongoDB.
// ============================================================
db.createCollection('eventos_carrera', {
  validator: {
    $jsonSchema: {
      bsonType: 'object',
      required: ['carrera_id', 'timestamp', 'tipo', 'datos'],
      properties: {
        carrera_id: {
          bsonType: 'string',
          description: 'Referencia a la carrera'
        },
        timestamp: {
          bsonType: 'date',
          description: 'Momento del evento en la carrera'
        },
        tipo: {
          enum: ['posicion', 'vuelta', 'bandera', 'clima', 'sector', 'pit_stop'],
          description: 'Categoría del evento'
        },
        datos: {
          bsonType: 'object',
          description: 'Payload variable según el tipo de evento'
        },
        leido_en: {
          bsonType: 'date',
          description: 'Cuándo lo leyó el motor del sistema'
        }
      }
    }
  }
});

// Índice compuesto para la consulta más frecuente:
// "dame los eventos de una carrera ordenados por tiempo"
db.eventos_carrera.createIndex({ carrera_id: 1, timestamp: 1 });
// Índice para filtrar por tipo dentro de una carrera
db.eventos_carrera.createIndex({ carrera_id: 1, tipo: 1, timestamp: 1 });

print('✅ Colección "eventos_carrera" creada con índices');


// ============================================================
// 3. COLECCIÓN: mercados
// Mercados creados por el administrador. Tienen un ciclo de
// vida: abierto → cerrado → liquidado. El motor es quien
// gestiona las transiciones.
// ============================================================
db.createCollection('mercados', {
  validator: {
    $jsonSchema: {
      bsonType: 'object',
      required: [
        'carrera_id', 'tipo', 'pregunta', 'opciones', 'estado',
        'abre_en', 'cierra_en', 'creado_en'
      ],
      properties: {
        carrera_id: {
          bsonType: 'string',
          description: 'Carrera a la que pertenece el mercado'
        },
        tipo: {
          enum: ['driver_first_position'],
          description: 'Tipo explícito de mercado implementado por la demo HU-07'
        },
        pregunta: {
          bsonType: 'string',
          description: 'Pregunta del mercado (ej: ¿Quién irá primero?)'
        },
        opciones: {
          bsonType: 'array',
          minItems: 2,
          items: { bsonType: 'string' },
          description: 'Opciones entre las que se apuesta'
        },
        estado: {
          enum: ['abierto', 'cerrado', 'liquidado'],
          description: 'Estado del ciclo de vida'
        },
        abre_en: {
          bsonType: 'date',
          description: 'Cuándo se abre la ventana de apuestas'
        },
        cierra_en: {
          bsonType: 'date',
          description: 'Cuándo cierra (abre_en + ventana de apuestas)'
        },
        observa_hasta: {
          bsonType: 'date',
          description: 'Cuándo termina la ventana de observación'
        },
        resultado: {
          bsonType: ['string', 'null'],
          description: 'Opción ganadora tras la liquidación'
        },
        puntos_otorgados: {
          bsonType: ['int', 'double', 'null'],
          description: 'Fórmula aplicada al liquidar'
        },
        liquidacion_error: {
          bsonType: 'string',
          description: 'Diagnóstico del último intento fallido de liquidación'
        },
        liquidacion_reintento_en: {
          bsonType: 'date',
          description: 'Momento mínimo del próximo intento automático'
        },
        liquidacion_lease: {
          bsonType: 'string',
          description: 'Token de adquisición temporal del mercado'
        },
        liquidacion_lease_hasta: {
          bsonType: 'date',
          description: 'Vencimiento del lease para recuperación tras caída'
        },
        creado_por: {
          bsonType: 'string',
          description: 'ID o nombre del administrador que lo creó'
        },
        creado_en: {
          bsonType: 'date',
          description: 'Timestamp de creación'
        },
        liquidado_en: {
          bsonType: ['date', 'null'],
          description: 'Timestamp de liquidación'
        }
      }
    }
  }
});

// El motor consulta constantemente:
// "¿hay mercados abiertos o por cerrar?"
db.mercados.createIndex({ estado: 1, cierra_en: 1 });
db.mercados.createIndex({ estado: 1, observa_hasta: 1, liquidacion_reintento_en: 1 });
// Índice por carrera para listar mercados de una sesión
db.mercados.createIndex({ carrera_id: 1, creado_en: -1 });

print('✅ Colección "mercados" creada con índices');


// ============================================================
// 4. COLECCIÓN: apuestas_contexto
// Contexto de cada apuesta desde el lado del motor. NO guarda
// dinero (eso vive en PostgreSQL). Solo guarda lo que el motor
// necesita para liquidar: qué apostó, a qué mercado, en qué
// momento del evento, y con qué snapshot de la carrera.
// ============================================================
db.createCollection('apuestas_contexto', {
  validator: {
    $jsonSchema: {
      bsonType: 'object',
      required: [
        'apuesta_id', 'mercado_id', 'usuario_id',
        'opcion', 'creada_en'
      ],
      properties: {
        apuesta_id: {
          bsonType: 'string',
          description: 'UUID generado por Pagos (referencia a Postgres)'
        },
        mercado_id: {
          bsonType: 'objectId',
          description: 'Referencia al mercado en esta BD'
        },
        usuario_id: {
          bsonType: 'string',
          description: 'UUID del usuario (referencia a Postgres)'
        },
        opcion: {
          bsonType: 'string',
          description: 'Opción elegida por el jugador'
        },
        monto: {
          bsonType: 'int',
          minimum: 1,
          description: 'Monto apostado en tokens (copia del valor en Postgres)'
        },
        snapshot_evento: {
          bsonType: 'object',
          description: 'Estado de la carrera al momento de apostar'
        },
        creada_en: {
          bsonType: 'date',
          description: 'Timestamp de creación'
        },
        liquidada: {
          bsonType: 'bool',
          description: 'Marca de liquidación'
        },
        gano: {
          bsonType: ['bool', 'null'],
          description: 'Resultado tras la liquidación'
        },
        puntos_obtenidos: {
          bsonType: ['double', 'int'],
          minimum: 0,
          description: 'Puntos confirmados por Pagos'
        }
      }
    }
  }
});

// Clave de idempotencia: no se puede registrar dos veces la misma apuesta
db.apuestas_contexto.createIndex({ apuesta_id: 1 }, { unique: true });
// Consultas por mercado (para liquidar en bloque)
db.apuestas_contexto.createIndex({ mercado_id: 1, liquidada: 1 });
// Consultas por usuario (para historial)
db.apuestas_contexto.createIndex({ usuario_id: 1, creada_en: -1 });

print('✅ Colección "apuestas_contexto" creada con índices');


// ============================================================
// RESUMEN FINAL
// ============================================================
print('');
print('========================================');
print('✅ Inicialización de apuestas_db completa');
print('========================================');
print('Colecciones creadas:');
print('  1. carreras             (metadata de carreras)');
print('  2. eventos_carrera      (datos crudos de OpenF1)');
print('  3. mercados             (mercados del admin)');
print('  4. apuestas_contexto    (contexto de apuestas del motor)');
print('');
print('Índices creados: 10');
print('Validaciones: 4 (una por colección)');
print('========================================');