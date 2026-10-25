package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/device"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
)

type DeviceRepository struct{ queries *dbgen.Queries }

func NewDeviceRepository(db dbgen.DBTX) *DeviceRepository {
	return &DeviceRepository{queries: dbgen.New(db)}
}
func uuid(id string) (pgtype.UUID, error) {
	var v pgtype.UUID
	if err := v.Scan(id); err != nil {
		return v, device.ErrInvalid
	}
	return v, nil
}
func nullable(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
func domainDevice(d dbgen.Device) device.Device {
	var location *string
	if d.Location.Valid {
		s := d.Location.String
		location = &s
	}
	b := d.ID.Bytes
	return device.Device{ID: fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), Name: d.Name, Type: d.Type, Location: location, Status: d.Status, Metadata: d.Metadata, CreatedAt: d.CreatedAt.Time, UpdatedAt: d.UpdatedAt.Time}
}
func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return device.ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return device.ErrConflict
	}
	// Deliberately discard driver text and SQL details at the storage boundary.
	return device.ErrRepository
}
func (r *DeviceRepository) Create(ctx context.Context, d device.Device) (device.Device, error) {
	id, err := uuid(d.ID)
	if err != nil {
		return device.Device{}, err
	}
	row, err := r.queries.CreateDevice(ctx, dbgen.CreateDeviceParams{ID: id, Name: d.Name, Type: d.Type, Location: nullable(d.Location), Status: d.Status, Metadata: d.Metadata})
	if err != nil {
		return device.Device{}, storageError(err)
	}
	return domainDevice(row), nil
}
func (r *DeviceRepository) List(ctx context.Context) ([]device.Device, error) {
	rows, err := r.queries.ListDevices(ctx)
	if err != nil {
		return nil, storageError(err)
	}
	result := make([]device.Device, 0, len(rows))
	for _, row := range rows {
		result = append(result, domainDevice(row))
	}
	return result, nil
}
func (r *DeviceRepository) Get(ctx context.Context, id string) (device.Device, error) {
	key, err := uuid(id)
	if err != nil {
		return device.Device{}, err
	}
	row, err := r.queries.GetDevice(ctx, key)
	if err != nil {
		return device.Device{}, storageError(err)
	}
	return domainDevice(row), nil
}
func (r *DeviceRepository) Update(ctx context.Context, id string, in device.Input) (device.Device, error) {
	key, err := uuid(id)
	if err != nil {
		return device.Device{}, err
	}
	metadata := []byte(`{}`)
	if in.Metadata.Set {
		metadata = in.Metadata.Value
	}
	row, err := r.queries.UpdateDevice(ctx, dbgen.UpdateDeviceParams{ID: key, SetName: in.Name.Set, Name: in.Name.Value, SetType: in.Type.Set, Type: in.Type.Value, SetLocation: in.Location.Set, Location: nullable(in.Location.Value), SetStatus: in.Status.Set, Status: in.Status.Value, SetMetadata: in.Metadata.Set, Metadata: metadata})
	if err != nil {
		return device.Device{}, storageError(err)
	}
	return domainDevice(row), nil
}
func (r *DeviceRepository) Delete(ctx context.Context, id string) error {
	key, err := uuid(id)
	if err != nil {
		return err
	}
	count, err := r.queries.DeleteDevice(ctx, key)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23503" && pgerr.ConstraintName == "incidents_device_id_fkey" {
			return device.ErrConflict
		}
		return storageError(err)
	}
	if count == 0 {
		return device.ErrNotFound
	}
	return nil
}
