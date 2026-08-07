package store

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/config"
	"github.com/BehiSecc/fetchdiff/internal/model"
	bolt "go.etcd.io/bbolt"
)

var (
	targetsBucket       = []byte("targets")
	namesBucket         = []byte("names")
	historyBucket       = []byte("history")
	notificationsBucket = []byte("notifications")
)

var ErrTargetChanged = errors.New("target changed during check")

type Store struct {
	paths config.Paths
}

func New(paths config.Paths) *Store {
	return &Store{paths: paths}
}

func (s *Store) Paths() config.Paths { return s.paths }

func (s *Store) Initialize() error {
	if err := config.EnsurePaths(s.paths); err != nil {
		return err
	}
	db, err := s.open()
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{targetsBucket, namesBucket, historyBucket, notificationsBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create database bucket: %w", err)
			}
		}
		return nil
	})
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close state database: %w", closeErr)
	}
	if err := os.Chmod(s.paths.Database, 0o600); err != nil {
		return fmt.Errorf("secure state database: %w", err)
	}
	return nil
}

func (s *Store) open() (*bolt.DB, error) {
	db, err := bolt.Open(s.paths.Database, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	return db, nil
}

func (s *Store) CreateTarget(target model.Target, entry model.HistoryEntry) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		names := tx.Bucket(namesBucket)
		if names.Get([]byte(strings.ToLower(target.Name))) != nil {
			return fmt.Errorf("target name %q already exists", target.Name)
		}
		if err := putJSON(tx.Bucket(targetsBucket), []byte(target.ID), target); err != nil {
			return err
		}
		if err := names.Put([]byte(strings.ToLower(target.Name)), []byte(target.ID)); err != nil {
			return fmt.Errorf("index target name: %w", err)
		}
		return putHistory(tx, entry)
	})
}

func (s *Store) UpdateTarget(target model.Target, expectedRevision uint64, entries ...model.HistoryEntry) error {
	return s.CommitTarget(target, expectedRevision, entries, nil)
}

func (s *Store) CommitTarget(target model.Target, expectedRevision uint64, entries []model.HistoryEntry, notifications []model.Notification) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		currentData := tx.Bucket(targetsBucket).Get([]byte(target.ID))
		if currentData == nil {
			return fmt.Errorf("target %q not found", target.Name)
		}
		var current model.Target
		if err := json.Unmarshal(currentData, &current); err != nil {
			return fmt.Errorf("decode current target: %w", err)
		}
		if current.Revision != expectedRevision {
			return ErrTargetChanged
		}
		if err := putJSON(tx.Bucket(targetsBucket), []byte(target.ID), target); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := putHistory(tx, entry); err != nil {
				return err
			}
		}
		for _, notification := range notifications {
			if notification.ID == "" {
				return errors.New("notification ID cannot be empty")
			}
			if len(notification.Deliveries) == 0 {
				continue
			}
			if err := putJSON(tx.Bucket(notificationsBucket), []byte(notification.ID), notification); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ClaimNotifications(now time.Time, owner string, lease time.Duration, limit int) ([]model.Notification, error) {
	if owner == "" {
		return nil, errors.New("notification lease owner cannot be empty")
	}
	if lease <= 0 {
		return nil, errors.New("notification lease must be positive")
	}
	if limit <= 0 {
		limit = 50
	}
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var claimed []model.Notification
	err = db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(notificationsBucket)
		type candidate struct {
			key          []byte
			notification model.Notification
		}
		var candidates []candidate
		if err := bucket.ForEach(func(key, value []byte) error {
			var notification model.Notification
			if err := json.Unmarshal(value, &notification); err != nil {
				return fmt.Errorf("decode notification: %w", err)
			}
			candidates = append(candidates, candidate{key: append([]byte(nil), key...), notification: notification})
			return nil
		}); err != nil {
			return err
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].notification.CreatedAt.Before(candidates[j].notification.CreatedAt)
		})
		earliest := make(map[string]string)
		for _, candidate := range candidates {
			for destination := range candidate.notification.Deliveries {
				if earliest[destination] == "" {
					earliest[destination] = candidate.notification.ID
				}
			}
		}
		for _, candidate := range candidates {
			if len(claimed) >= limit {
				break
			}
			notification := candidate.notification
			if notification.LeaseOwner != "" && notification.LeaseUntil.After(now) {
				continue
			}
			due := false
			for destination, delivery := range notification.Deliveries {
				if earliest[destination] == notification.ID && !delivery.NextAttemptAt.After(now) {
					due = true
					break
				}
			}
			if !due {
				continue
			}
			notification.LeaseOwner = owner
			notification.LeaseUntil = now.Add(lease)
			if err := putJSON(bucket, candidate.key, notification); err != nil {
				return err
			}
			claimed = append(claimed, notification)
		}
		return nil
	})
	return claimed, err
}

func (s *Store) SaveDelivery(notificationID, owner, destination string, state *model.DeliveryState, leaseUntil time.Time) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(notificationsBucket)
		data := bucket.Get([]byte(notificationID))
		if data == nil {
			return fmt.Errorf("notification %q not found", notificationID)
		}
		var notification model.Notification
		if err := json.Unmarshal(data, &notification); err != nil {
			return fmt.Errorf("decode notification: %w", err)
		}
		if notification.LeaseOwner != owner {
			return fmt.Errorf("notification %q lease was lost", notificationID)
		}
		if _, exists := notification.Deliveries[destination]; !exists {
			return fmt.Errorf("notification destination %q not found", destination)
		}
		if state == nil {
			delete(notification.Deliveries, destination)
		} else {
			notification.Deliveries[destination] = *state
		}
		if len(notification.Deliveries) == 0 {
			return bucket.Delete([]byte(notificationID))
		}
		if !leaseUntil.IsZero() {
			notification.LeaseUntil = leaseUntil
		}
		return putJSON(bucket, []byte(notificationID), notification)
	})
}

func (s *Store) ReleaseNotification(notificationID, owner string) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(notificationsBucket)
		data := bucket.Get([]byte(notificationID))
		if data == nil {
			return nil
		}
		var notification model.Notification
		if err := json.Unmarshal(data, &notification); err != nil {
			return fmt.Errorf("decode notification: %w", err)
		}
		if notification.LeaseOwner != owner {
			return nil
		}
		notification.LeaseOwner = ""
		notification.LeaseUntil = time.Time{}
		return putJSON(bucket, []byte(notificationID), notification)
	})
}

func (s *Store) Notifications() ([]model.Notification, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var notifications []model.Notification
	err = db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(notificationsBucket).ForEach(func(_, value []byte) error {
			var notification model.Notification
			if err := json.Unmarshal(value, &notification); err != nil {
				return fmt.Errorf("decode notification: %w", err)
			}
			notifications = append(notifications, notification)
			return nil
		})
	})
	sort.Slice(notifications, func(i, j int) bool { return notifications[i].CreatedAt.Before(notifications[j].CreatedAt) })
	return notifications, err
}

func (s *Store) NotificationCounts() (events, destinations int, err error) {
	notifications, err := s.Notifications()
	if err != nil {
		return 0, 0, err
	}
	for _, notification := range notifications {
		destinations += len(notification.Deliveries)
	}
	return len(notifications), destinations, nil
}

func (s *Store) Target(nameOrID string) (model.Target, error) {
	db, err := s.open()
	if err != nil {
		return model.Target{}, err
	}
	defer db.Close()
	var target model.Target
	err = db.View(func(tx *bolt.Tx) error {
		id := []byte(nameOrID)
		if indexed := tx.Bucket(namesBucket).Get([]byte(strings.ToLower(nameOrID))); indexed != nil {
			id = append([]byte(nil), indexed...)
		}
		data := tx.Bucket(targetsBucket).Get(id)
		if data == nil {
			return fmt.Errorf("target %q not found", nameOrID)
		}
		return json.Unmarshal(data, &target)
	})
	return target, err
}

func (s *Store) Targets() ([]model.Target, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var targets []model.Target
	err = db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(targetsBucket).ForEach(func(_, value []byte) error {
			var target model.Target
			if err := json.Unmarshal(value, &target); err != nil {
				return fmt.Errorf("decode target: %w", err)
			}
			targets = append(targets, target)
			return nil
		})
	})
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, err
}

func (s *Store) History(targetID string) ([]model.HistoryEntry, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var entries []model.HistoryEntry
	err = db.View(func(tx *bolt.Tx) error {
		prefix := []byte(targetID + "\x00")
		cursor := tx.Bucket(historyBucket).Cursor()
		for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
			var entry model.HistoryEntry
			if err := json.Unmarshal(value, &entry); err != nil {
				return fmt.Errorf("decode history: %w", err)
			}
			entries = append(entries, entry)
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].CheckedAt.After(entries[j].CheckedAt) })
	return entries, err
}

func (s *Store) PutSnapshot(hash string, content []byte) error {
	path, err := s.snapshotPath(hash)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure snapshot: %w", err)
	}
	zw := gzip.NewWriter(tmp)
	if _, err := zw.Write(content); err != nil {
		zw.Close()
		tmp.Close()
		return fmt.Errorf("compress snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return fmt.Errorf("finish snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return nil
		}
		return fmt.Errorf("store snapshot: %w", err)
	}
	return nil
}

func (s *Store) Snapshot(hash string) ([]byte, error) {
	path, err := s.snapshotPath(hash)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("read snapshot header: %w", err)
	}
	defer zr.Close()
	content, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("decompress snapshot: %w", err)
	}
	return content, nil
}

func (s *Store) snapshotPath(hash string) (string, error) {
	if len(hash) != 64 {
		return "", fmt.Errorf("invalid snapshot hash %q", hash)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("invalid snapshot hash: %w", err)
	}
	return filepath.Join(s.paths.Snapshots, hash[:2], hash+".gz"), nil
}

func putJSON(bucket *bolt.Bucket, key []byte, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := bucket.Put(key, data); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func putHistory(tx *bolt.Tx, entry model.HistoryEntry) error {
	if entry.ID == "" {
		entry.ID = randomID()
	}
	key := []byte(fmt.Sprintf("%s\x00%020d\x00%s", entry.TargetID, entry.CheckedAt.UnixNano(), entry.ID))
	return putJSON(tx.Bucket(historyBucket), key, entry)
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
