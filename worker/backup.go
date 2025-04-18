/*
 * SPDX-FileCopyrightText: © Hypermode Inc. <hello@hypermode.com>
 * SPDX-License-Identifier: Apache-2.0
 */

package worker

import (
	"context"
	"math"

	"github.com/golang/glog"
	"github.com/pkg/errors"

	"github.com/dgraph-io/badger/v4"
	"github.com/hypermodeinc/dgraph/v24/protos/pb"
	"github.com/hypermodeinc/dgraph/v24/x"
)

// predicateSet is a map whose keys are predicates. It is meant to be used as a set.
type predicateSet map[string]struct{}

// Manifest records backup details, these are values used during restore.
// Since is the timestamp from which the next incremental backup should start (it's set
// to the readTs of the current backup).
// Groups are the IDs of the groups involved.
type Manifest struct {
	// Type is the type of backup, either full or incremental.
	Type string `json:"type"`
	// SinceTsDeprecated is kept for backward compatibility. Use readTs instead of sinceTs.
	SinceTsDeprecated uint64 `json:"since"`
	// ReadTs is the timestamp at which this backup was taken. This would be
	// the since timestamp for the next incremental backup.
	ReadTs uint64 `json:"read_ts"`
	// Groups is the map of valid groups to predicates at the time the backup was created.
	Groups map[uint32][]string `json:"groups"`
	// BackupId is a unique ID assigned to all the backups in the same series
	// (from the first full backup to the last incremental backup).
	BackupId string `json:"backup_id"`
	// BackupNum is a monotonically increasing number assigned to each backup in
	// a series. The full backup as BackupNum equal to one and each incremental
	// backup gets assigned the next available number. Used to verify the integrity
	// of the data during a restore.
	BackupNum uint64 `json:"backup_num"`
	// Version specifies the Dgraph version, the backup was taken on. For the backup taken on older
	// versions (<= 20.11), the predicates in Group map do not have namespace. Version will be zero
	// for older versions.
	Version int `json:"version"`
	// Path is the name of the backup directory to which this manifest belongs to.
	Path string `json:"path"`
	// Encrypted indicates whether this backup was encrypted or not.
	Encrypted bool `json:"encrypted"`
	// DropOperations lists the various DROP operations that took place since the last backup.
	// These are used during restore to redo those operations before applying the backup.
	DropOperations []*pb.DropOperation `json:"drop_operations"`
	// Compression keeps track of the compression that was used for the data.
	Compression string `json:"compression"`
}

// ValidReadTs function returns the valid read timestamp. The backup can have
// the readTs=0 if the backup was done on an older version of dgraph. The
// SinceTsDecprecated is kept for backward compatibility.
func (m *Manifest) ValidReadTs() uint64 {
	if m.ReadTs == 0 {
		return m.SinceTsDeprecated
	}
	return m.ReadTs
}

type MasterManifest struct {
	Manifests []*Manifest
}

func (m *Manifest) getPredsInGroup(gid uint32) predicateSet {
	preds, ok := m.Groups[gid]
	if !ok {
		return nil
	}

	predSet := make(predicateSet)
	for _, pred := range preds {
		predSet[pred] = struct{}{}
	}
	return predSet
}

// GetCredentialsFromRequest extracts the credentials from a backup request.
func GetCredentialsFromRequest(req *pb.BackupRequest) *x.MinioCredentials {
	return &x.MinioCredentials{
		AccessKey:    req.GetAccessKey(),
		SecretKey:    req.SecretKey,
		SessionToken: req.SessionToken,
		Anonymous:    req.GetAnonymous(),
	}
}

func StoreExport(request *pb.ExportRequest, dir string, key x.Sensitive) error {
	db, err := badger.OpenManaged(badger.DefaultOptions(dir).
		WithSyncWrites(false).
		WithValueThreshold(1 << 10).
		WithNumVersionsToKeep(math.MaxInt32).
		WithEncryptionKey(key))
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			glog.Warningf("error closing the DB: %v", err)
		}
	}()

	_, err = exportInternal(context.Background(), request, db, true)
	return errors.Wrapf(err, "cannot export data inside DB at %s", dir)
}
