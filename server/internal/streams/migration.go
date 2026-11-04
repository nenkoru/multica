package streams

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// OriginMigration marks the summarize turn that moves a stream off the old
// board (§12 "Миграция без остановки", step 2).
const OriginMigration = "migration"

// MaxMigrateIssues bounds one migration turn: in_progress and blocked issues
// only, backlog does not move.
const MaxMigrateIssues = 40

var errMigrationOpen = errors.New("migrate: this stream already has an open migration turn")

var issueIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// NormalizeMigrate checks and dedups the old issues a migration turn reads.
func NormalizeMigrate(ids []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		if !issueIdentifier.MatchString(id) {
			return nil, fmt.Errorf("migrate: %q is not an issue identifier like RUSL-123", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > MaxMigrateIssues {
		return nil, fmt.Errorf("migrate: %d issues, at most %d (backlog does not move)", len(out), MaxMigrateIssues)
	}
	return out, nil
}

// MigrationBrief is the trigger of the migration turn.
func MigrationBrief(ids []string) string {
	return "Переезд с доски: старые задачи потока " + strings.Join(ids, ", ") + "."
}

const migrationInstructions = "\n## Переезд с доски (§12)\n\n" +
	"Это первый ход потока после переезда. Старая доска живёт как есть, backlog не переносится.\n\n" +
	"- Прочитай задачи из «Почему ход»: `multica issue get <номер>` и последние ветки комментариев. Это единственное исключение из правила «историю не читай» — только эти задачи.\n" +
	"- `summary` — состояние потока из них: решения, открытые хвосты, метрика, что ждёт Руслана и по какой категории мандата.\n" +
	"- Незаконченную работу поставь очередью в `next`, по шагу на хвост, каждый с `moves`. Если задача ещё идёт у агента (активный run), не дублируй её: верни `wait` вида `issue` на её номер.\n" +
	"- В `result` перечисли: `перенесены: …` (стали шагами или ожиданием) и `migrated: …` (закрываются без переноса).\n" +
	"- Сами задачи не закрывай, не переназначай и не комментируй: метку `migrated` ставит переносящий после хода.\n"
