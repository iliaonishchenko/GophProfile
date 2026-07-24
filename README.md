# GophProfile

GophProfile микросервис для управления аватарками пользователей написанный с использованием Go, Chi, PostgreSQL,
RabbitMQ, and MinIO.

## локальный запуск

запуск в докере следующей командой

```bash
docker compose up --build
```

- Web UI and API: <http://localhost:8080>
- RabbitMQ: <http://localhost:15672>
- MinIO: <http://localhost:9001>
- PostgreSQL: `localhost:5432`

дефолтные креды в `compose.yaml`.

## команды

```bash
task generate-server  # сгенерировать сервер и модели из OpenAPI
task generate-mocks   # сгенерировать mock-реализации доменных интерфейсов
task test             # запуск тестов
task cover            # запуск юнит тестов с измерением покрытия
task vet              # запуск go vet
task lint             # запуск golangci-lint
task build            # собрать server и worker
task run              # запуск HTTP server
task run-worker       # запуск image воркера
```

## процесс обработки

1. API валидирует и загружает изображение в MinIO
2. метаданные сохраняются в PostgreSQL
3. `avatar.uploaded` событие публикуется в RabbitMQ топик
4. воркер создаёт `100x100` и `300x300` JPEG thumbnails
5. ключи и статус сохраняются в PostgreSQL.
6. удаление делает софт-делет метаданнх и публикует событие `avatar.deleted`
