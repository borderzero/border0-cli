package preference

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Data_SetOrg(t *testing.T) {
	now := time.Now()
	fiveDaysAgo := now.AddDate(0, 0, -5)
	testOrg := Org{
		ID:        "123",
		Subdomain: "test",
		LastUsed:  fiveDaysAgo,
	}

	tests := []struct {
		name           string
		givenData      Data
		givenOrgToSet  *Org
		wantOrgsLength int
	}{
		{
			name:           "nil input",
			givenData:      Data{},
			givenOrgToSet:  nil,
			wantOrgsLength: 0,
		},
		{
			name:      "input has <nil> id and subdomain",
			givenData: Data{},
			givenOrgToSet: &Org{
				ID:        "<nil>",
				Subdomain: "<nil>",
			},
			wantOrgsLength: 0,
		},
		{
			name: "update existing org's last used field",
			givenData: Data{
				Orgs: map[string]Org{
					testOrg.ID: testOrg,
				},
			},
			givenOrgToSet: &Org{
				ID:        testOrg.ID,
				Subdomain: testOrg.Subdomain,
			},
			wantOrgsLength: 1,
		},
		{
			name: "add new org to the list",
			givenData: Data{
				Orgs: map[string]Org{
					testOrg.ID: testOrg,
				},
			},
			givenOrgToSet: &Org{
				ID:        "456",
				Subdomain: "test2",
			},
			wantOrgsLength: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.givenData.SetOrg(test.givenOrgToSet)

			require.Equal(t, test.wantOrgsLength, len(test.givenData.Orgs))

			if test.wantOrgsLength > 0 {
				gotOrg := test.givenData.Org(test.givenOrgToSet.ID)

				assert.Equal(t, test.givenOrgToSet.ID, gotOrg.ID)
				assert.Equal(t, test.givenOrgToSet.Subdomain, gotOrg.Subdomain)
				assert.True(t, gotOrg.LastUsed.After(now))
			}
		})
	}
}

func Test_Data_RecentlyUsedOrgs(t *testing.T) {
	testOrg123 := Org{
		ID:        "123",
		Subdomain: "test",
		LastUsed:  time.Now().AddDate(0, 0, -5),
	}
	testOrg456 := Org{
		ID:        "456",
		Subdomain: "test2",
		LastUsed:  time.Now(),
	}
	testData := Data{
		Orgs: map[string]Org{
			testOrg123.ID: testOrg123,
			testOrg456.ID: testOrg456,
			"<nil>": {
				ID:        "<nil>",
				Subdomain: "<nil>",
				LastUsed:  time.Time{},
			},
			"": {
				ID:        "",
				Subdomain: "",
				LastUsed:  time.Time{},
			},
		},
	}

	tests := []struct {
		name  string
		given int
		want  Orgs
	}{
		{
			name:  "return all orgs",
			given: -1,
			want:  Orgs{testOrg456, testOrg123},
		},
		{
			name:  "return only 1 org",
			given: 1,
			want:  Orgs{testOrg456},
		},
		{
			name:  "want 3 orgs but only 2 orgs exist",
			given: 3,
			want:  Orgs{testOrg456, testOrg123},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotOrgs := testData.RecentlyUsedOrgs(test.given)

			assert.Equal(t, test.want, gotOrgs)
		})
	}
}
