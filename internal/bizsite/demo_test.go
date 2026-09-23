// SPDX-License-Identifier: Apache-2.0

package bizsite

import (
	"reflect"
	"testing"
)

// The live case: Bistro's sample, published as a real business.
func TestDemoFieldsFindsATemplatesSampleContent(t *testing.T) {
	c := ByKey("bistro").Defaults
	if got := DemoFields(c); !reflect.DeepEqual(got, []string{"name", "tagline", "about", "hours", "cta", "services"}) {
		t.Fatalf("DemoFields(bistro sample) = %v", got)
	}
}

// Switching design keeps content, so another template's sample under this one
// is still sample content.
func TestDemoFieldsChecksEveryTemplate(t *testing.T) {
	c := Content{Name: ByKey("forge").Defaults.Name}
	if got := DemoFields(c); !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("forge's sample name under another design → %v, want [name]", got)
	}
}

// One seed per field, so each comparison is shown to decide on its own.
func TestDemoFieldsDecidesEachFieldOnItsOwn(t *testing.T) {
	d := ByKey("bistro").Defaults
	for field, c := range map[string]Content{
		"name":     {Name: d.Name},
		"tagline":  {Tagline: d.Tagline},
		"about":    {About: d.About},
		"hours":    {Hours: d.Hours},
		"cta":      {CTA: d.CTA},
		"services": {Services: []Service{{Title: d.Services[0].Title}}},
	} {
		if got := DemoFields(c); !reflect.DeepEqual(got, []string{field}) {
			t.Errorf("only %s is sample → %v", field, got)
		}
	}
}

// Real content, and empty content, are not sample content.
func TestDemoFieldsIgnoresRealAndEmptyContent(t *testing.T) {
	for _, c := range []Content{
		{},
		{Name: "Johal Studio", Tagline: "Websites for people", Hours: "Mon–Fri 09:00–17:00", Services: []Service{{Title: "Design"}}},
	} {
		if got := DemoFields(c); len(got) != 0 {
			t.Errorf("DemoFields(%+v) = %v, want none", c, got)
		}
	}
}
