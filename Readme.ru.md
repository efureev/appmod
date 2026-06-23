# appmod — Абстрактный модуль приложения

[English](Readme.md) | [Русский](Readme.ru.md)

[![Test](https://github.com/efureev/appmod/actions/workflows/test.yml/badge.svg)](https://github.com/efureev/appmod/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/efureev/appmod)](https://goreportcard.com/report/github.com/efureev/appmod)
[![codecov](https://codecov.io/gh/efureev/appmod/branch/master/graph/badge.svg)](https://codecov.io/gh/efureev/appmod)
[![Go Reference](https://pkg.go.dev/badge/github.com/efureev/appmod.svg)](https://pkg.go.dev/github.com/efureev/appmod)
[![License](https://img.shields.io/github/license/efureev/appmod)](LICENSE)

Маленький строительный блок без внешних зависимостей для организации приложения
в виде набора **модулей** с общим жизненным циклом, поддерживающим контекст
(`Init` / `Destroy`), и хуками жизненного цикла
(`BeforeStart` / `AfterStart` / `BeforeDestroy` / `AfterDestroy`).

## Возможности

- Минимализм и отсутствие внешних зависимостей.
- Чёткое разделение **контракта** (интерфейсы) и **базовой реализации**.
- Жизненный цикл с поддержкой контекста: `Init(ctx)` / `Destroy(ctx)`.
- Четыре набора хуков; на каждую фазу можно зарегистрировать несколько хуков, выполняемых по порядку.
- Хуки способны прервать запуск/остановку через возврат `error`.
- Защита идемпотентности: повторный `Init` или `Destroy` до `Init` возвращает sentinel-ошибку.
- Явная **машина состояний** жизненного цикла (`Created → Initializing → Running → Destroying → Destroyed`, плюс `Failed`), доступная через `State()`.
- **Учёт контекста**: после отмены контекста оставшиеся хуки не выполняются.
- **Атомарный `Init`**: ошибка любого стартового хука (или отмена контекста) запускает автоматический откат (teardown-хуки выполняются в обратном порядке) и оставляет модуль в `StateFailed`.
- Встраиваемый `BaseAppModule` — реализуйте свой модуль через встраивание.
- **Потокобезопасность**: жизненный цикл, регистрация хуков и доступ к конфигу защищены мьютексом.
- **Защита от паник в хуках**: паника в хуке перехватывается и возвращается как ошибка.
- Узкие интерфейсы возможностей (`Configurable` / `Lifecycle` / `HookRegistry`), составляющие `AppModule`.
- Конструктор `New(opts ...Option)` с функциональными опциями.

## Требования

- Go **1.24** или новее.

## Установка

```bash
go get github.com/efureev/appmod/v2
```

## Обзор API

```go
// AppModuleConfig описывает конфигурацию модуля.
type AppModuleConfig interface {
Name() string
Version() string
}

// HookFunc — хук жизненного цикла.
type HookFunc func(ctx context.Context, mod AppModule) error

// Узкие интерфейсы возможностей.
type Configurable interface {
SetConfig(config AppModuleConfig)
Config() AppModuleConfig
}

type Lifecycle interface {
Init(ctx context.Context) error
Destroy(ctx context.Context) error
}

type HookRegistry interface {
BeforeStart(fn HookFunc)
AfterStart(fn HookFunc)
BeforeDestroy(fn HookFunc)
AfterDestroy(fn HookFunc)
}

// AppModule составлен из узких интерфейсов выше.
type AppModule interface {
Configurable
Lifecycle
HookRegistry
}
```

`BaseAppModule` безопасен для конкурентного использования несколькими горутинами,
а паника внутри хука перехватывается и возвращается как ошибка.

Жизненный цикл — явная машина состояний, доступная через `State()`:

```
Created → Initializing → Running → Destroying → Destroyed
```

Повторный `Init` на работающем модуле возвращает `ErrAlreadyInitialized`; вызов
`Destroy` на неработающем модуле возвращает `ErrNotInitialized`. Уничтоженный
(или завершившийся с ошибкой) модуль можно инициализировать повторно.

`Init` **атомарен**: если любой стартовый хук (`BeforeStart` или `AfterStart`)
возвращает ошибку или контекст отменён, модуль автоматически откатывается,
выполняя teardown-хуки (`BeforeDestroy`, затем `AfterDestroy`) в обратном порядке
регистрации, и переходит в `StateFailed`. Ошибки отката объединяются с исходной
причиной через `errors.Join`. Таким образом, модуль никогда не остаётся
полу-запущенным: `Init` либо полностью успешен (`StateRunning`), либо
завершается с ошибкой (`StateFailed`).

### Конструкторы

| Функция                    | Описание                                                 |
|----------------------------|----------------------------------------------------------|
| `NewConfig(name, version)` | Создаёт `Config` с заданными именем и версией.           |
| `DefaultConfig()`          | Возвращает `Config` по умолчанию (`App Module`, `v0.0.1`). |
| `New(opts ...Option)`      | Создаёт `*BaseAppModule`, настроенный функциональными опциями. |

Функциональные опции: `WithConfig`, `WithBeforeStart`, `WithAfterStart`,
`WithBeforeDestroy`, `WithAfterDestroy`.

```go
mod := appmod.New(
    appmod.WithConfig(appmod.NewConfig("Cache", "v1.0.0")),
    appmod.WithBeforeStart(func(ctx context.Context, m appmod.AppModule) error {
        return nil
    }),
)
```

## Использование

### Базовый пример

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/efureev/appmod/v2"
)

func main() {
	ctx := context.Background()

	mod := &appmod.BaseAppModule{}
	mod.SetConfig(appmod.NewConfig("My Module", "v1.0.0"))

	// Регистрируем хуки жизненного цикла.
	mod.BeforeStart(func(ctx context.Context, m appmod.AppModule) error {
		fmt.Printf("запуск %s %s\n", m.Config().Name(), m.Config().Version())
		return nil
	})
	mod.BeforeDestroy(func(ctx context.Context, m appmod.AppModule) error {
		fmt.Printf("остановка %s\n", m.Config().Name())
		return nil
	})

	if err := mod.Init(ctx); err != nil {
		log.Fatalf("ошибка инициализации: %v", err)
	}
	defer func() {
		if err := mod.Destroy(ctx); err != nil {
			log.Fatalf("ошибка завершения: %v", err)
		}
	}()

	// ... логика приложения ...
}
```

### Свой модуль через встраивание

```go
type CacheModule struct {
appmod.BaseAppModule
// ваши собственные поля...
}

func NewCacheModule() *CacheModule {
m := &CacheModule{}
m.SetConfig(appmod.NewConfig("Cache", "v1.0.0"))
return m
}
```

### Прерывание запуска

Если хук `BeforeStart` возвращает ошибку, `Init(ctx)` вернёт её (обёрнутой), и
модуль считается незапущенным:

```go
mod.BeforeStart(func(ctx context.Context, m appmod.AppModule) error {
return fmt.Errorf("некорректная конфигурация")
})

if err := mod.Init(ctx); err != nil {
// обрабатываем ошибку
}
```

То же самое относится к `BeforeDestroy` и `Destroy(ctx)`.

## Разработка

В репозитории есть `Makefile` и `docker-compose.yml`, поэтому локальный тулчейн Go
не обязателен.

```bash
make help     # список доступных команд
make test     # запуск линтеров и тестов
make gotest   # тесты с детектором гонок и покрытием
make lint     # запуск golangci-lint
make fmt      # форматирование кода
```

Запуск тестов напрямую:

```bash
go test -race ./...
```

## Лицензия

Распространяется на условиях [лицензии MIT](LICENSE).
