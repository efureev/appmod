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
- **Именованные, приоритетные и удаляемые хуки** (`Hook`, `AddHook` / `RemoveHook`): в рамках фазы хуки выполняются в
  порядке возрастания приоритета.
- Хуки получают узкое read-only представление `HookModule` (конфиг/имя/состояние) вместо полного модуля — непрозрачное значение, которое нельзя привести обратно к `Lifecycle` или `HookRegistry`.
- Хуки способны прервать запуск/остановку через возврат `error`, который возвращается как типизированный `HookError` (
  фаза, индекс, имя, модуль).
- Опциональное **структурированное логирование на уровне модуля** (`slog`): переходы жизненного цикла и длительность
  фаз.
- Защита идемпотентности: повторный `Init` или `Destroy` до `Init` возвращает sentinel-ошибку.
- Явная **машина состояний** жизненного цикла (`Created → Initializing → Running → Destroying → Destroyed`, плюс
  `Failed`), доступная через `State()`.
- **Учёт контекста**: после отмены контекста оставшиеся хуки не выполняются.
- **Атомарный `Init`**: ошибка любого стартового хука (или отмена контекста) запускает автоматический откат
  (компенсации `AddCleanup` разматываются в обратном порядке) и оставляет модуль в `StateFailed`.
- Встраиваемый `BaseAppModule` — реализуйте свой модуль через встраивание.
- **Потокобезопасность**: жизненный цикл, регистрация хуков и доступ к конфигу защищены мьютексом.
- **Защита от паник в хуках**: паника в хуке перехватывается и возвращается как ошибка.
- Узкие интерфейсы возможностей (`Configurable` / `Named` / `Stateful` / `Lifecycle` / `HookRegistry`), составляющие
  `AppModule`.
- Конструктор `New(opts ...Option)` с функциональными опциями.
- **Наблюдение за остановкой** (`AppContext.Done()`): модуль реагирует на начало шатдауна без блокировки и не дожидаясь собственного `Destroy`.
- **Оркестратор модулей** `Manager`: запуск в порядке зависимостей (топологический) с параллельным стартом независимых
  модулей, остановка послойно в обратном порядке и тоже параллельно внутри слоя, обнаружение циклов зависимостей,
  graceful shutdown по `SIGINT`/`SIGTERM` и опциональные health-проверки.
- **Компенсации, привязанные к жизненному циклу** (`AddCleanup`, `SubscribeModule`): освобождение регистрируется рядом с
  захватом и выполняется на `Destroy` и при откате.

## Требования

- Go **1.24** или новее.

## Установка

```bash
go get github.com/efureev/appmod/v3
```

## Обзор API

```go
// AppModuleConfig описывает конфигурацию модуля.
type AppModuleConfig interface {
    Name() string
    Version() string
}

// HookFunc — хук жизненного цикла; получает узкое read-only представление.
type HookFunc func (ctx context.Context, mod HookModule) error

// Узкие интерфейсы возможностей.
type Configurable interface {
  SetConfig(config AppModuleConfig)
  Config() AppModuleConfig
}

type Named interface {
    Name() string
}

type Stateful interface {
    State() State
}

// HookModule — узкое read-only представление, передаваемое в HookFunc.
type HookModule interface {
  Configurable
  Named
  Stateful
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
  AddHook(phase Phase, hook Hook)
  RemoveHook(phase Phase, name string) bool
}

// AppModule составлен из узких интерфейсов выше.
type AppModule interface {
  Configurable
  Named
  Stateful
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

Модуль переходит в `StateRunning` только после того, как `Init` завершил **обе**
стартовые фазы, поэтому хук `AfterStart` всё ещё наблюдает `StateInitializing`.
Так сделано намеренно: публикация `StateRunning` до окончания запуска позволяла
конкурентному `Destroy` пройти проверку состояния и снести модуль на середине
старта, выполнив teardown-хуки дважды.

Teardown-хуки всегда видят `StateDestroying` — и когда их вызвал `Destroy`, и
когда откат внутри `Init`, — поэтому хук может ветвиться по `State()`, не
разбираясь, каким путём его позвали.

Zero value действительно готов к использованию: `Config()` возвращает
`DefaultConfig()`, пока не вызван `SetConfig`, а `Name()` следует за ним в
точности, так что `m.Config().Name()` внутри хука безопасен всегда. Ненастроенный
модуль поэтому называется `App Module`.

`Init` **атомарен**: если любой стартовый хук (`BeforeStart` или `AfterStart`)
возвращает ошибку или контекст отменён, модуль автоматически откатывается и
переходит в `StateFailed`. Ошибки отката объединяются с исходной причиной через
`errors.Join`. Таким образом, модуль никогда не остаётся полу-запущенным: `Init`
либо полностью успешен (`StateRunning`), либо завершается с ошибкой
(`StateFailed`).

Откат разматывает компенсации, зарегистрированные через `AddCleanup`, в обратном
порядке — а **не** teardown-хуки. Teardown-хуки описывают, как остановить
*дозапустившийся* модуль; выполнять их после незавершённого старта — значит
закрывать пул, который никто не открывал. Регистрируйте освобождение рядом с
захватом, и хук, который не отработал, ничего не оставит на откат:

```go
mod.BeforeStart(func(ctx context.Context, m appmod.HookModule) error {
    pool, err := openPool()
    if err != nil {
        return err
    }
    mod.AddCleanup(func(context.Context) error { return pool.Close() })

    return nil
})
```

Компенсации выполняются и при штатном `Destroy` — между фазами `BeforeDestroy` и
`AfterDestroy`.

### Конструкторы

| Функция                    | Описание                                                       |
|----------------------------|----------------------------------------------------------------|
| `NewConfig(name, version)` | Создаёт `Config` с заданными именем и версией.                 |
| `DefaultConfig()`          | Возвращает `Config` по умолчанию (`App Module`, `v0.0.1`).     |
| `New(opts ...Option)`      | Создаёт `*BaseAppModule`, настроенный функциональными опциями. |

Функциональные опции: `WithConfig`, `WithModuleLogger`, `WithHook`,
`WithBeforeStart`, `WithAfterStart`, `WithBeforeDestroy`, `WithAfterDestroy`.

### Именованные приоритетные хуки

Помимо анонимных помощников `BeforeStart` / `AfterStart` / ... хуки можно
регистрировать с именем и приоритетом и удалять позже. В рамках фазы хуки
выполняются в порядке возрастания приоритета (при равенстве сохраняется порядок
регистрации):

```go
mod.AddHook(appmod.PhaseBeforeStart, appmod.Hook{
  Name:     "open-db",
  Priority: -10, // выполнится раньше
  Run: func (ctx context.Context, m appmod.HookModule) error { return nil },
})
mod.RemoveHook(appmod.PhaseBeforeStart, "open-db")
```

Ошибка хука возвращается как `*HookError` с фазой, индексом, именем хука и именем
модуля; она разворачивается до исходной ошибки, поэтому `errors.Is` / `errors.As`
продолжают работать.

`AddHook`, `RemoveHook` и `WithHook` **паникуют**, если фаза не одна из четырёх
определённых. Значение `Phase` вне диапазона — ошибка программиста, а
альтернатива (молча выбросить хук) превращает логику запуска в код, который
никогда не выполняется, и заметить это нечем. Фазу из конфигурации или из
внешнего формата сначала проверяйте через `Phase.Valid()`.

### Логирование на уровне модуля

Привяжите `*slog.Logger` к модулю (через `WithModuleLogger` или `SetLogger`),
чтобы получать структурированные логи переходов жизненного цикла и длительности
фаз. По умолчанию используется no-op обработчик.

```go
mod := appmod.New(
  appmod.WithConfig(appmod.NewConfig("Cache", "v1.0.0")),
  appmod.WithBeforeStart(func (ctx context.Context, m appmod.AppModule) error {
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

	"github.com/efureev/appmod/v3"
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
mod.BeforeStart(func (ctx context.Context, m appmod.AppModule) error {
    return fmt.Errorf("некорректная конфигурация")
})

if err := mod.Init(ctx); err != nil {
// обрабатываем ошибку
}
```

То же самое относится к `BeforeDestroy` и `Destroy(ctx)`.

### Оркестрация модулей

Для приложения из нескольких взаимозависимых модулей `Manager` запускает их в
порядке зависимостей (топологическом) — независимые модули параллельно — и
останавливает в обратном порядке:

```go
mgr := appmod.NewManager(
    appmod.WithShutdownTimeout(10*time.Second),
)
_ = mgr.Register("db", db)
_ = mgr.Register("cache", cache, "db") // cache зависит от db
_ = mgr.Register("api", api, "cache", "db") // api зависит от обоих

// Старт, ожидание SIGINT/SIGTERM и graceful-остановка в обратном порядке.
if err := mgr.Run(context.Background()); err != nil {
    log.Fatal(err)
}
```

`Register(name, module, deps...)` валидирует имена и зависимости; `Start`
возвращает `ErrUnknownDependency` для отсутствующих зависимостей и
`ErrDependencyCycle` при цикле в графе. Неудачный `Start` откатывает уже
запущенные модули. Модули, реализующие `HealthChecker`, можно проверить через
`mgr.Health(ctx)`.

Остановка зеркалит запуск: `Stop` переиспользует те же слои зависимостей, идёт по
ним в обратном порядке и гасит каждый слой **конкурентно**, поэтому остановка
стоит максимума по слою, а не суммы по всем модулям — именно сумма и вылезала за
бюджет `WithShutdownTimeout` в приложении из в основном независимых модулей.
Порядок между слоями не изменился: модуль всегда останавливается раньше тех, от
кого зависит.

Любой модуль может узнать о начале остановки через `AppContext`, который
инжектировал менеджер, — это нужно фоновому циклу, чтобы перестать брать новую
работу до того, как дойдёт очередь до его слоя:

```go
go func() {
    for {
        select {
        case <-m.AppContext().Done():
            return // шатдаун начался: новую работу не берём
        case job := <-jobs:
            process(job)
        }
    }
}()
```

`Done()` закрывается при любой остановке, которую выполняет менеджер, включая
откат неудачного `Start`. `AppContext.Context()` даёт тот же сигнал в виде
контекста, от которого безопасно порождать контексты запросов.

`Run` по умолчанию ждёт `SIGINT`, `SIGTERM` или `SIGQUIT` (набор заменяется
через `WithSignals`), отмену контекста либо `Manager.Shutdown()`. **Второй
сигнал во время остановки завершает процесс** с кодом 128+signum — это
единственный выход оператора из зависшей очистки: обработчик сигналов
установлен, и второй Ctrl-C иначе уйдёт в никуда. Отключается через
`WithForceOnSecondSignal(false)`, но тогда зависший модуль остановит только
SIGKILL. `Manager.ExitCode()` возвращает соответствующий код для `os.Exit`.

`Run` **одноразов**: последовательность остановки выполняется ровно один раз,
поэтому повторный вызов возвращает `ErrAlreadyRun`. Менеджер, управляемый через
`Start`/`Stop`, по-прежнему можно перезапускать.

`Start` **не реентерабелен**: вызов на уже стартующем или работающем менеджере
возвращает `ErrAlreadyStarted` и не трогает работающие модули. Именно эта
защита делает случайный или конкурентный второй `Start` безобидным — без неё
второй вызов падал на уже инициализированных модулях, считал это провалом
старта и откатывался, останавливая модули, поднятые первым `Start`. После
`Stop` менеджер можно запустить снова.

`Run` делегирует механику graceful shutdown (перехват сигналов и остановку,
ограниченную по времени) пакету
[`github.com/efureev/go-shutdown`](https://github.com/efureev/go-shutdown): он
завершается по `SIGINT`/`SIGTERM` или отмене контекста, затем выполняет `Stop`
с ограничением `WithShutdownTimeout` и возвращает `shutdown.ErrShutdownTimeout`,
если остановка не уложилась в таймаут. `Stop` учитывает отмену контекста между
модулями: при срабатывании таймаута он сразу прерывается, а не продолжает
работать в отдельной горутине; неостановленные модули сохраняются для
последующего `Stop`.

### Общение между модулями

Граф зависимостей задаёт только *порядок* старта; для общения в рантайме
`Manager` создаёт общий `AppContext` (`EventBus`, `Registry` и логгер) и
внедряет его в каждый модуль, реализующий `ContextAware`. `BaseAppModule` уже
реализует этот интерфейс, поэтому встраивающий модуль получает общие сервисы
через `m.AppContext()`. Снаружи те же экземпляры доступны через
`Manager.EventBus()` и `Manager.Registry()`.

Есть два взаимодополняющих механизма:

**Registry — pull (запрос/ответ).** Модуль *предоставляет* реализацию
контракта-интерфейса, а зависимый модуль её *запрашивает*. Контракт ключуется
по Go-типу, поэтому потребитель зависит от интерфейса, а не от конкретного
модуля. `Require[T]` гарантированно находит поставщика, если потребитель
объявил зависимость `Manager` от предоставляющего модуля (его `AfterStart`
выполняется раньше).

```go
type DB interface{ Query(ctx context.Context, key string) (string, error) }

// модуль db — в его AfterStart:
_ = appmod.Provide[DB](m.AppContext().Registry, m) // m реализует DB

// модуль cache (зависит от "db") — в его AfterStart:
db, err := appmod.Require[DB](m.AppContext().Registry)
```

`Provide` отклоняет nil-реализацию с `ErrNilImplementation` — обычная причина
такой ошибки — конструктор, вернувший `(nil, err)`, чью ошибку не проверили.
Сообщение об этом в `Provide` оставляет ошибку там, где она сделана: сам
`Require` не паникует никогда, он возвращает ошибку.

Об одной асимметрии стоит знать: `Provide` и `Require` сообщают о nil-реестре
через `ErrNilRegistry`, а `Revoke` возвращает голый `bool` и отдаёт `false` и для
nil-реестра, и для контракта, который никогда не предоставляли. По возвращаемому
значению эти случаи неразличимы; если это важно, проверяйте реестр на nil сами.

**EventBus — push (без ответа).** Модуль *подписывается* на тип-значение, а
любой модуль *публикует* значения этого типа. Доставка синхронная,
типобезопасная, защищена от паник и объединяет ошибки подписчиков через
`errors.Join`.

События ключуются по Go-типу. Если значение попадает в `Publish` через
интерфейсную переменную (`any`, `error`, доменный интерфейс), событие
доставляется как по **динамическому** типу, так и по статическому — поэтому
проброс события через обёртку не теряет его молча. Подписчики на интерфейсный
тип продолжают работать, и ни один подписчик не вызывается дважды.

Изнутри модуля используйте `SubscribeModule`: он привязывает подписку к
жизненному циклу модуля и снимает её на `Destroy`. Обычный `Subscribe` возвращает
`Unsubscribe`, который нужно самому сохранить и вызвать, — забыли, и модуль после
остановки и повторного запуска оказывается подписан дважды, так что каждое
событие доставляется дважды, прибавляя по доставке на рестарт. Это аналог
`Revoke`, только для `EventBus`.

```go
// внутри стартового хука модуля, встраивающего appmod.BaseAppModule:
err := appmod.SubscribeModule(&m.BaseAppModule, func(ctx context.Context, e UserCreated) error {
    // инвалидация, реакция, ...
    return nil
})
```

Всё остальное, что нужно освободить при остановке модуля, регистрируйте рядом с
местом захвата через `AddCleanup`; очистки выполняются в обратном порядке
регистрации на `Destroy` и при откате неудачного `Init`.

```go
type UserCreated struct{ ID string }

// подписчик (например, cache, во время старта):
unsub, _ := appmod.Subscribe(m.AppContext().Bus, func(_ context.Context, e UserCreated) error {
    // инвалидация, реакция, ...
    return nil
})
defer unsub() // либо снять подписку в BeforeDestroy

// публикатор (например, api, позже):
_ = appmod.Publish(ctx, m.AppContext().Bus, UserCreated{ID: "user:1"})
```

Правило: используйте **Registry**, когда данными владеет один модуль, а вызвавшему
нужен ответ (`api → cache → db`); используйте **EventBus**, чтобы оповестить о
факте любое число слушателей без ожидания ответа.

## Примеры

Готовые к запуску примеры приложений лежат в [`examples/`](examples) и
покрывают все возможности пакета:

| Пример                        | Что демонстрирует                                                                                                                                             |
|-------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`basic`](examples/basic)     | Жизненный цикл одного модуля: конфигурация, четыре хука и машина состояний `Created → Running → Destroyed`.                                                   |
| [`hooks`](examples/hooks)     | Опции `New(...)`, логирование через `slog`, именованные/приоритетные хуки (`AddHook`/`RemoveHook`), типизированная ошибка `HookError` и автоматический откат. |
| [`manager`](examples/manager) | Оркестрация графа модулей через `Manager` и общение модулей: доступ к данным `api → cache → db` через `Provide`/`Require` и событие `UserCreated` через `EventBus`. |

```bash
go run ./examples/basic
go run ./examples/hooks
go run ./examples/manager
```

## Структура пакета

Пакет разбит на небольшие файлы с чёткой зоной ответственности:

| Файл         | Назначение                                                                                                                     |
|--------------|--------------------------------------------------------------------------------------------------------------------------------|
| `appmod.go`  | Документация пакета и compile-time проверки контрактов.                                                                        |
| `module.go`  | `AppModule` и узкие интерфейсы `Configurable` / `Named` / `Stateful` / `Lifecycle` / `HookRegistry`, `HookFunc`, `HookModule`. |
| `config.go`  | `AppModuleConfig`, тип-значение `Config` и `NewConfig` / `DefaultConfig`.                                                      |
| `state.go`   | Перечисление состояний `State` и его метод `String`.                                                                           |
| `errors.go`  | Sentinel-ошибки жизненного цикла.                                                                                              |
| `base.go`    | Встраиваемая реализация `BaseAppModule`.                                                                                       |
| `hook.go`    | Типы `Phase` и `Hook`, типизированная ошибка `HookError`.                                                                      |
| `options.go` | Функциональные опции и конструктор `New`.                                                                                      |
| `manager.go` | Оркестратор `Manager`: запуск/остановка по зависимостям, graceful shutdown, health-проверки.                                   |
| `eventbus.go` | Типобезопасный `EventBus` для уведомлений без ответа (`Subscribe`/`Publish`).                                                 |
| `registry.go` | Типобезопасный `Registry` для доступа между модулями по контракту (`Provide`/`Require`/`Revoke`).                            |
| `appcontext.go` | Общий `AppContext` (`EventBus` + `Registry` + логгер + широковещание остановки) и возможность `ContextAware`.        |

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
