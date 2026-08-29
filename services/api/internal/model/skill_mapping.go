package model

import "time"

type SkillMappingTarget struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Kind           string         `json:"kind"`
	DirectoryPath  string         `json:"directoryPath"`
	Status         string         `json:"status"`
	Error          string         `json:"error,omitempty"`
	Mappings       []SkillMapping `json:"mappings"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	LastVerifiedAt *time.Time     `json:"lastVerifiedAt,omitempty"`
}

type SkillMapping struct {
	TargetID       string     `json:"targetId"`
	SkillID        string     `json:"skillId"`
	SkillName      string     `json:"skillName"`
	SourcePath     string     `json:"sourcePath"`
	LinkName       string     `json:"linkName"`
	LinkPath       string     `json:"linkPath"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	LastVerifiedAt *time.Time `json:"lastVerifiedAt,omitempty"`
}

type CreateSkillMappingTargetRequest struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	DirectoryPath string   `json:"directoryPath"`
	SkillIDs      []string `json:"skillIds"`
}

type UpdateSkillMappingTargetRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type SkillMappingIDsRequest struct {
	SkillIDs []string `json:"skillIds"`
}
