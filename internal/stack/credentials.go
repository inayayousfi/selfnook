package stack

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
)

// CredentialGroups lists the groups' names: the core's, then the apps' in
// registry order. Apps that can fill the same role share a group name,
// listed once.
func CredentialGroups(apps []*app.App) []string {
	var names []string
	for _, a := range append([]*app.App{app.Core}, apps...) {
		if a.Credentials != nil && !slices.Contains(names, a.Credentials.Name) {
			names = append(names, a.Credentials.Name)
		}
	}
	return names
}

// Credentials shows one group's saved values and offers to change them.
// Without a group, it lists the groups.
func (s *Stack) Credentials(name string) error {
	if name == "" {
		s.UI.Say("Choose a group: " + strings.Join(CredentialGroups(s.Apps), ", ") + "\nPasswords and API keys appear only when you choose a group.")
		return nil
	}
	if !slices.Contains(CredentialGroups(s.Apps), name) {
		return fmt.Errorf("Unknown group: %s.", name)
	}
	ses, found, err := s.open()
	if err != nil {
		return err
	}
	if !found {
		return errNoSetup
	}
	if err := ses.assignRoles(false); err != nil {
		return err
	}
	var owner *app.App
	for _, a := range append([]*app.App{app.Core}, ses.active()...) {
		if a.Credentials != nil && a.Credentials.Name == name {
			owner = a
		}
	}
	if owner == nil {
		s.UI.Say("No saved values for " + name + ".")
		return nil
	}
	group, values := owner.Credentials, app.ValuesFor(ses.saved, owner)

	var shown []app.CredentialField
	for _, field := range group.Fields {
		if values.Has(field.Key) {
			shown = append(shown, field)
		}
	}
	if len(shown) == 0 {
		s.UI.Say("No saved values for " + name + ".")
		return nil
	}
	var listing []string
	var fields []flow.Field
	for _, field := range shown {
		listing = append(listing, field.Label+": "+values.Get(field.Key))
		if field.Kind != app.Fixed {
			fields = append(fields, newValueField(field))
		}
	}
	screen := flow.Screen{Body: strings.Join(listing, "\n")}
	if len(fields) == 0 {
		return flow.Show(s.UI, screen)
	}
	screen.Fields = []flow.Field{{Key: "change", Prompt: "Change these values?", Kind: flow.YesNo, Default: "no"}}
	answers, err := s.UI.Ask(screen)
	if err != nil || answers["change"] != "yes" {
		return err
	}
	if answers, err = s.UI.Ask(flow.Screen{Fields: fields}); err != nil {
		return err
	}
	return ses.saveChanges(owner, values, shown, answers)
}

func newValueField(field app.CredentialField) flow.Field {
	label := strings.ToLower(field.Label)
	result := flow.Field{Key: field.Key, Prompt: "New " + label + " [Enter keeps it]"}
	switch field.Kind {
	case app.Password:
		result.Kind = flow.Secret
		result.Repeat = "Repeat new " + label
		result.Mismatch = "Values do not match."
	case app.APIKey:
		result.Check = func(value string) (string, error) {
			if value == "" {
				return "", nil
			}
			return flow.ValidAPIKey(value)
		}
	}
	return result
}

func (ses *session) saveChanges(owner *app.App, values app.Values, fields []app.CredentialField, answers flow.Answers) error {
	changed, usedByStack := false, false
	var recorded []string
	for _, field := range fields {
		value := answers[field.Key]
		if value == "" || value == values.Get(field.Key) {
			continue
		}
		values.Set(field.Key, value)
		changed = true
		usedByStack = usedByStack || field.UsedByStack
		if !field.UsedByStack {
			recorded = append(recorded, field.Label)
		}
	}
	if !changed {
		ses.UI.Say("No changes.")
		return nil
	}
	if err := ses.save(); err != nil {
		return err
	}
	ses.UI.Say("Saved.")
	if usedByStack {
		ses.UI.Say("The stack uses the changed values after its next start.")
	}
	if len(recorded) > 0 {
		pronoun := "it"
		if len(recorded) > 1 {
			pronoun = "them"
		}
		ses.UI.Say("Selfnook keeps only a record of " + app.List(recorded) + ". Change " + pronoun + " in " + owner.Title + " as well.")
	}
	return nil
}
