package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spk/spk-mail/internal/storage"
)

func (s *Stub) ListProfiles(ctx context.Context) ([]ProfileDTO, error) {
	rows, err := s.Store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProfileDTO{ID: r.ID, Name: r.Name, Color: r.Color, SortOrder: r.SortOrder, Muted: r.Muted})
	}
	return out, nil
}

func (s *Stub) AddProfile(ctx context.Context, req AddProfileRequest) (ProfileDTO, error) {
	id, err := s.Store.InsertProfile(ctx, storage.ProfileRow{
		Name: req.Name, Color: req.Color, SortOrder: 0,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return ProfileDTO{}, err
	}
	row, err := s.Store.GetProfile(ctx, id)
	if err != nil {
		return ProfileDTO{}, err
	}
	return ProfileDTO{ID: row.ID, Name: row.Name, Color: row.Color, SortOrder: row.SortOrder, Muted: row.Muted}, nil
}

func (s *Stub) UpdateProfile(ctx context.Context, req UpdateProfileRequest) (ProfileDTO, error) {
	if err := s.Store.UpdateProfile(ctx, req.ID, req.Name, req.Color); err != nil {
		return ProfileDTO{}, err
	}
	row, err := s.Store.GetProfile(ctx, req.ID)
	if err != nil {
		return ProfileDTO{}, err
	}
	return ProfileDTO{ID: row.ID, Name: row.Name, Color: row.Color, SortOrder: row.SortOrder, Muted: row.Muted}, nil
}

func (s *Stub) DeleteProfile(ctx context.Context, id int64) error {
	if err := s.Store.DeleteProfile(ctx, id); err != nil {
		if errors.Is(err, storage.ErrProfileInUse) {
			return fmt.Errorf("%w (profile %d)", ErrProfileInUse, id)
		}
		return err
	}
	return nil
}

func (s *Stub) SetProfileMuted(ctx context.Context, id int64, muted bool) error {
	return s.Store.SetProfileMuted(ctx, id, muted)
}
