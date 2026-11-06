package installrun

import (
	"errors"
	"os"

	"brain/internal/redact"
)

func privateDestinations(policy *redact.Policy, values ...string) error {
	for _, value := range values {
		if policy.Match(value) {
			return errors.New("installation paths or arguments contain protected text; choose safe values before installing")
		}
	}
	return nil
}

func safeError(err error) string {
	var path *os.PathError
	if errors.As(err, &path) {
		return path.Err.Error()
	}
	var link *os.LinkError
	if errors.As(err, &link) {
		return link.Err.Error()
	}
	return err.Error()
}

func (result receipt) safe() receipt {
	policy := result.policy
	if policy == nil {
		policy = redact.Default()
	}
	clean := func(value string) string { return policy.Apply(value).Text }
	result.Status = clean(result.Status)
	result.Binary = clean(result.Binary)
	result.Backup = clean(result.Backup)
	result.BackupsDir = clean(result.BackupsDir)
	result.SettingsFile = clean(result.SettingsFile)
	skillFiles := map[string]string{}
	for name, file := range result.SkillFiles {
		skillFiles[clean(name)] = clean(file)
	}
	result.SkillFiles = skillFiles
	result.DailyJob.Name = clean(result.DailyJob.Name)
	result.DailyJob.Time = clean(result.DailyJob.Time)
	result.DailyJob.Files = append([]string(nil), result.DailyJob.Files...)
	for n, file := range result.DailyJob.Files {
		result.DailyJob.Files[n] = clean(file)
	}
	result.Selection.State = clean(result.Selection.State)
	result.Selection.Config = clean(result.Selection.Config)
	result.Message = clean(result.Message)
	result.Completed = append([]string{}, result.Completed...)
	for n, step := range result.Completed {
		result.Completed[n] = clean(step)
	}
	return result
}
