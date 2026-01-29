Ops-CLI: Kubernetes & FluxCD Bootstrapper

Ops-CLI — это инструмент на Go для автоматического развертывания кластеров Kubernetes и настройки GitOps (FluxCD) с подходом Zero Local Dependencies.

Утилита позволяет поднять инфраструктуру "с чистого листа" и восстановить состояние кластера (DR), используя S3 как единый источник правды для стейта, секретов и бэкапов.

Возможности

    Zero Local Dependencies: Не требует установки Docker, Ansible или Python на машине оператора. CLI компилирует и отправляет self-contained runner на Bastion-хост.

    S3 State Persistence: Хранит привязки дисков и IP-адреса в S3, гарантируя идемпотентность при пересоздании ВМ.

    FluxCD Bootstrap: Автоматическая установка Flux и инъекция ключей расшифровки (SOPS/AGE) и доступов к приватному репозиторию.

    Dynamic Configuration: Управление режимами работы приложений (Restore Mode / Normal Mode) через инъекцию ConfigMap без коммитов в Git.

    Reliability: Запуск долгих операций в screen-сессиях на удаленном сервере.

Требования

Для работы нужны только переменные окружения. Локально ничего, кроме бинарника, не требуется.

Облако (CLO)
export CLO_AUTH_TOKEN="..."
export CLO_OBJECT_ID="..."

Хранилище (S3)
export S3_ENDPOINT="..."
export S3_ACCESS_KEY="..."
export S3_SECRET_KEY="..."
export S3_BUCKET="..."

Секреты для Flux
export GITHUB_TOKEN="..." # Доступ к Git-репозиторию
export SOPS_AGE_KEY="..." # Ключ для расшифровки секретов
export ACME_EMAIL="..." # Email для Let's Encrypt

Быстрый старт (Disaster Recovery Flow)

Весь процесс восстановления кластера укладывается в 4 шага:

    Подготовка инфраструктуры
    Создание ВМ, балансировщика и генерация State-файла в S3.
    ./ops-cli -cluster -name production -nocheck

    Монтирование дисков
    CLI читает State из S3, находит реальные UUID дисков и монтирует их в нужные директории (для PostgreSQL/Redis).
    ./ops-cli -mount -name production

    Развертывание Kubernetes
    Запуск Kubespray внутри изолированного раннера на Bastion-хосте.
    ./ops-cli -deploy -name production

    Bootstrap GitOps
    Установка FluxCD и инъекция ключей для доступа к зашифрованным секретам в S3.
    ./ops-cli -flux -name production

Управление состоянием (Advanced)

Переключение режимов (ConfigMap Injection)
Мы не правим Git для временных задач. Например, чтобы переключить базу данных в режим восстановления из дампа:

./ops-cli -name production -cmcreate 'pg:pg-db-values-switch:{"dataSource":{..."options":["--type=immediate"]...}}'

Внешняя проверка (Load Testing)
Запуск временного кластера в Yandex Cloud с K6 Operator для нагрузочного тестирования восстановленного сервиса:
./ops-cli -hload target.domain.com -script k6scripts/test.js

Структура репозитория

/cli — Исходный код Ops-CLI и Runner'а.
/flux — Системные манифесты Flux (Bootstrap).
/fluxcd — Манифесты приложений и инфраструктуры (Payload).
.github/workflows — Пайплайн полного цикла восстановления.

Проект создан для демонстрации подхода "Infrastructure as Code" без локальных зависимостей.
# cli
Deploy k8s [CLO](https://lk.clo.ru/sign/up/?ref_id=1067484)
