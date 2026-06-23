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
- Встраиваемый `BaseAppModule` — реализуйте свой модуль через встраивание.

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

// AppModule описывает жизненный цикл модуля.
type AppModule interface {
SetConfig(config AppModuleConfig)
Config() AppModuleConfig

Init(ctx context.Context) error
Destroy(ctx context.Context) error

BeforeStart(fn HookFunc)
AfterStart(fn HookFunc)
BeforeDestroy(fn HookFunc)
AfterDestroy(fn HookFunc)
}
```

Жизненный цикл защищён внутренним флагом состояния. Повторный `Init` возвращает
`ErrAlreadyInitialized`; вызов `Destroy` на неинициализированном модуле возвращает
`ErrNotInitialized`.

### Конструкторы

| Функция                    | Описание                                                 |
|----------------------------|----------------------------------------------------------|
| `NewConfig(name, version)` | Создаёт конфиг с заданными именем и версией.             |
| `DefaultConfig()`          | Возвращает конфиг по умолчанию (`App Module`, `v0.0.1`). |

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
