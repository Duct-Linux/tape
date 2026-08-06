package utils

import (
	"sort"

	"github.com/spf13/viper"
)

func RepoSort(repos map[string]*viper.Viper) []*viper.Viper {
	// sort repos by priority
	// default priority is 10
	// 11 is higher than 10

	// get all priorities
	priorities := make(map[string]int)
	for key, repo := range repos {
		priorities[key] = repo.GetInt("repo.priority")
	}

	// sort priorities
	var sortedRepos []*viper.Viper
	for _, repo := range repos {
		sortedRepos = append(sortedRepos, repo)
	}

	sort.Slice(sortedRepos, func(i, j int) bool {
		return priorities[sortedRepos[i].GetString("key")] > priorities[sortedRepos[j].GetString("key")]
	})

	return sortedRepos
}
