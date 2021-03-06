// Copyright 2014 Globo.com. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package config provide configuration facilities, handling configuration
// files in yaml format.
package config

import (
	"fmt"
	"github.com/howeyc/fsnotify"
	"gopkg.in/v1/yaml"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	configs map[interface{}]interface{}
	mut     sync.RWMutex
)

func readConfigBytes(data []byte, out interface{}) error {
	return yaml.Unmarshal(data, out)
}

// ReadConfigBytes receives a slice of bytes and builds the internal
// configuration object.
//
// If the given slice is not a valid yaml file, ReadConfigBytes returns a
// non-nil error.
func ReadConfigBytes(data []byte) error {
	mut.Lock()
	defer mut.Unlock()
	return readConfigBytes(data, &configs)
}

func readConfigFile(filePath string, out interface{}) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}
	return readConfigBytes(data, out)
}

// ReadConfigFile reads the content of a file and calls ReadConfigBytes to
// build the internal configuration object.
//
// It returns error if it can not read the given file or if the file contents
// is not valid yaml.
func ReadConfigFile(filePath string) error {
	return readConfigFile(filePath, &configs)
}

// ReadAndWatchConfigFile reads and watchs for changes in the configuration
// file. Whenever the file change, and its contents are valid YAML, the
// configuration gets updated. With this function, daemons that use this
// package may reload configuration without restarting.
func ReadAndWatchConfigFile(filePath string) error {
	err := ReadConfigFile(filePath)
	if err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	err = w.Watch(filePath)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case e := <-w.Event:
				if e.IsModify() {
					var tmp map[interface{}]interface{}
					if readConfigFile(filePath, &tmp) == nil {
						mut.Lock()
						configs = tmp
						mut.Unlock()
					}
				}
			case <-w.Error: // just ignore errors
			}
		}
	}()
	return nil
}

// Bytes serialize the configuration in YAML format.
func Bytes() ([]byte, error) {
	mut.RLock()
	defer mut.RUnlock()
	b, err := yaml.Marshal(configs)
	return b, err
}

// WriteConfigFile writes the configuration to the disc, using the given path.
// The configuration is serialized in YAML format.
//
// This function will create the file if it does not exist, setting permissions
// to "perm".
func WriteConfigFile(filePath string, perm os.FileMode) error {
	b, err := Bytes()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// Get returns the value for the given key, or an eror if the key is undefined.
//
// The key is composed by all the key names separated by :, in case of nested
// keys. For example, suppose we have the following configuration yaml:
//
//   databases:
//     mysql:
//       host: localhost
//       port: 3306
//
// The key "databases:mysql:host" would return "localhost", while the key
// "port" would return an error.
//
// Get will expand the value with environment values, ex.:
//
//   mongo: $MONGOURI
//
// If there is an environment variable MONGOURI=localhost/test, the key "mongo"
// would return "localhost/test"
func Get(key string) (interface{}, error) {
	keys := strings.Split(key, ":")
	mut.RLock()
	defer mut.RUnlock()
	conf, ok := configs[keys[0]]
	if !ok {
		return nil, fmt.Errorf("key %q not found", key)
	}
	for _, k := range keys[1:] {
		conf, ok = conf.(map[interface{}]interface{})[k]
		if !ok {
			return nil, fmt.Errorf("key %q not found", key)
		}
	}
	if v, ok := conf.(string); ok {
		return os.ExpandEnv(v), nil
	}
	return conf, nil
}

// GetString works like Get, but doing a string type assertion before return
// the value.
//
// It returns error if the key is undefined or if it is not a string.
func GetString(key string) (string, error) {
	value, err := Get(key)
	if err != nil {
		return "", err
	}
	if v, ok := value.(string); ok {
		return v, nil
	}
	return "", &invalidValue{key, "string"}
}

// GetInt works like Get, but doing a int type assertion before return
// the value.
//
// It returns error if the key is undefined or if it is not a int.
func GetInt(key string) (int, error) {
	value, err := Get(key)
	if err != nil {
		return 0, err
	}
	if v, ok := value.(int); ok {
		return v, nil
	} else if v, ok := value.(string); ok {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return int(i), nil
		}
	}
	return 0, &invalidValue{key, "int"}
}

// GetUint parses and returns an unsigned integer from the config file.
func GetUint(key string) (uint, error) {
	value, err := Get(key)
	if err != nil {
		return 0, err
	}
	if v, ok := value.(int); ok {
		if v < 0 {
			return 0, &invalidValue{key, "uint"}
		}
		return uint(v), nil
	}
	return 0, &invalidValue{key, "uint"}
}

// GetDuration parses and returns a duration from the config file. It may be an
// integer or a number specifying the amount of nanoseconds.
//
// Here are some examples of valid durations:
//
//  - 1h30m0s
//  - 1e9 (one second)
//  - 100e6 (one hundred milliseconds)
//  - 1 (one nanosecond)
//  - 1000000000 (one billion nanoseconds, or one second)
func GetDuration(key string) (time.Duration, error) {
	value, err := Get(key)
	if err != nil {
		return 0, err
	}
	switch value.(type) {
	case int:
		return time.Duration(value.(int)), nil
	case float64:
		return time.Duration(value.(float64)), nil
	case string:
		if value, err := time.ParseDuration(value.(string)); err == nil {
			return value, nil
		}
	}
	return 0, &invalidValue{key, "duration"}
}

// GetBool does a type assertion before returning the requested value
func GetBool(key string) (bool, error) {
	value, err := Get(key)
	if err != nil {
		return false, err
	}
	if v, ok := value.(bool); ok {
		return v, nil
	}
	return false, &invalidValue{key, "boolean"}
}

// GetList works like Get, but returns a slice of strings instead. It must be
// written down in the config as YAML lists.
//
// Here are two example of YAML lists:
//
//   names:
//     - Mary
//     - John
//     - Paul
//     - Petter
//
// If GetList find an item that is not a string (for example 5.08734792), it
// will convert the item.
func GetList(key string) ([]string, error) {
	value, err := Get(key)
	if err != nil {
		return nil, err
	}
	switch value.(type) {
	case []interface{}:
		v := value.([]interface{})
		result := make([]string, len(v))
		for i, item := range v {
			switch item.(type) {
			case int:
				result[i] = strconv.Itoa(item.(int))
			case bool:
				result[i] = strconv.FormatBool(item.(bool))
			case float64:
				result[i] = strconv.FormatFloat(item.(float64), 'f', -1, 64)
			case string:
				result[i] = item.(string)
			default:
				result[i] = fmt.Sprintf("%v", item)
			}
		}
		return result, nil
	case []string:
		return value.([]string), nil
	}
	return nil, &invalidValue{key, "list"}
}

// mergeMaps takes two maps and merge its keys and values recursively.
//
// In case of conflicts, the function picks value from map2.
func mergeMaps(map1, map2 map[interface{}]interface{}) map[interface{}]interface{} {
	result := make(map[interface{}]interface{})
	for k, v2 := range map2 {
		if v1, ok := map1[k]; !ok {
			result[k] = v2
		} else {
			map1, ok1 := v1.(map[interface{}]interface{})
			map2, ok2 := v2.(map[interface{}]interface{})
			if ok1 && ok2 {
				result[k] = mergeMaps(map1, map2)
			} else {
				result[k] = v2
			}
		}
	}
	for k, v := range map1 {
		if v2, ok := map2[k]; !ok {
			result[k] = v
		} else {
			map1, ok1 := v.(map[interface{}]interface{})
			map2, ok2 := v2.(map[interface{}]interface{})
			if ok1 && ok2 {
				result[k] = mergeMaps(map1, map2)
			}
		}
	}
	return result
}

// Set redefines or defines a value for a key. The key has the same format that
// it has in Get and GetString.
//
// Values defined by this function affects only runtime informatin, nothing
// defined by Set is persisted in the filesystem or any database.
func Set(key string, value interface{}) {
	parts := strings.Split(key, ":")
	last := map[interface{}]interface{}{
		parts[len(parts)-1]: value,
	}
	for i := len(parts) - 2; i >= 0; i-- {
		last = map[interface{}]interface{}{
			parts[i]: last,
		}
	}
	mut.Lock()
	configs = mergeMaps(configs, last)
	mut.Unlock()
}

// Unset removes a key from the configuration map. It returns error if the key
// is not defined.
//
// Calling this function does not remove a key from a configuration file, only
// from the in-memory configuration object.
func Unset(key string) error {
	var i int
	var part string
	mut.Lock()
	defer mut.Unlock()
	m := configs
	parts := strings.Split(key, ":")
	for i, part = range parts {
		if item, ok := m[part]; ok {
			if nm, ok := item.(map[interface{}]interface{}); ok && i < len(parts)-1 {
				m = nm
			} else {
				break
			}
		} else {
			return fmt.Errorf("Key %q not found", key)
		}
	}
	delete(m, part)
	return nil
}

type invalidValue struct {
	key  string
	kind string
}

func (e *invalidValue) Error() string {
	return fmt.Sprintf("value for the key %q is not a %s", e.key, e.kind)
}
