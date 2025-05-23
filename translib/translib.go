////////////////////////////////////////////////////////////////////////////////
//                                                                            //
//  Copyright 2019 Broadcom. The term Broadcom refers to Broadcom Inc. and/or //
//  its subsidiaries.                                                         //
//                                                                            //
//  Licensed under the Apache License, Version 2.0 (the "License");           //
//  you may not use this file except in compliance with the License.          //
//  You may obtain a copy of the License at                                   //
//                                                                            //
//     http://www.apache.org/licenses/LICENSE-2.0                             //
//                                                                            //
//  Unless required by applicable law or agreed to in writing, software       //
//  distributed under the License is distributed on an "AS IS" BASIS,         //
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  //
//  See the License for the specific language governing permissions and       //
//  limitations under the License.                                            //
//                                                                            //
////////////////////////////////////////////////////////////////////////////////

/*
Package translib implements APIs like Create, Get, Subscribe etc.

to be consumed by the north bound management server implementations

This package takes care of translating the incoming requests to

Redis ABNF format and persisting them in the Redis DB.

It can also translate the ABNF format to YANG specific JSON IETF format

This package can also talk to non-DB clients.
*/

package translib

import (
	"context"
	"sync"

	"github.com/Azure/sonic-mgmt-common/translib/db"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	"github.com/openconfig/ygot/ygot"
)

// Write lock for all write operations to be synchronized
var writeMutex = &sync.Mutex{}

type ErrSource int

const (
	ProtoErr ErrSource = iota
	AppErr
)

type TranslibFmtType int

const (
	TRANSLIB_FMT_IETF_JSON TranslibFmtType = iota
	TRANSLIB_FMT_YGOT
)

type UserRoles struct {
	Name  string
	Roles []string
}

type SetRequest struct {
	Path             string
	Payload          []byte
	User             UserRoles
	AuthEnabled      bool
	ClientVersion    Version
	DeleteEmptyEntry bool
}

type SetResponse struct {
	ErrSrc ErrSource
	Err    error
}

type QueryParameters struct {
	Depth   uint     // range 1 to 65535, default is <U+0093>0<U+0094> i.e. all
	Content string   // all, config, non-config(REST)/state(GNMI), operational(GNMI only)
	Fields  []string // list of fields from NBI
}

type GetRequest struct {
	Path          string
	FmtType       TranslibFmtType
	User          UserRoles
	AuthEnabled   bool
	ClientVersion Version
	QueryParams   QueryParameters
	Ctxt          context.Context
}

type GetResponse struct {
	Payload   []byte
	ValueTree ygot.ValidatedGoStruct
	ErrSrc    ErrSource
}

type ActionRequest struct {
	Path          string
	Payload       []byte
	User          UserRoles
	AuthEnabled   bool
	ClientVersion Version
}

type ActionResponse struct {
	Payload []byte
	ErrSrc  ErrSource
}

// BulkRequestEntry - Entry for BulkRequest
type BulkRequestEntry struct {
	Entry                 SetRequest
	Operation             int
	ResourceCheckOnDelete bool
}

// BulkRequest - Will be used by Northbounds to send Bulk Request.
type BulkRequest struct {
	Request       []BulkRequestEntry
	User          UserRoles
	AuthEnabled   bool
	ClientVersion Version
}

// BulkResponseEntry - Entry for BulkResponse
type BulkResponseEntry struct {
	Entry     SetResponse
	Operation int
}

// BulkResponse - Will be used by Northbounds to receive Bulk Response.
type BulkResponse struct {
	Response []BulkResponseEntry
}

type ModelData struct {
	Name string
	Org  string
	Ver  string
}

// initializes logging and app modules
func init() {
	log.Flush()
}

// Create - Creates entries in the redis DB pertaining to the path and payload
func Create(req SetRequest) (SetResponse, error) {
	var keys []db.WatchKeys
	var resp SetResponse
	path := req.Path
	payload := req.Payload
	if !isAuthorizedForSet(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Create Operation",
			Path:   path,
		}
	}

	log.Info("Create request received with path =", path)
	log.Info("Create request received with payload =", string(payload))

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	err = appInitialize(app, appInfo, path, &payload, nil, CREATE)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	d, err := db.NewDB(getDBOptions(db.ConfigDB))

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	defer d.DeleteDB()

	keys, err = (*app).translateCreate(d)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.StartTx(keys, appInfo.tablesToWatch)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	resp, err = (*app).processCreate(d)

	if err != nil {
		d.AbortTx()
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.CommitTx()

	if err != nil {
		resp.ErrSrc = AppErr
	}

	return resp, err
}

// Update - Updates entries in the redis DB pertaining to the path and payload
func Update(req SetRequest) (SetResponse, error) {
	var keys []db.WatchKeys
	var resp SetResponse
	path := req.Path
	payload := req.Payload
	if !isAuthorizedForSet(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Update Operation",
			Path:   path,
		}
	}

	log.Info("Update request received with path =", path)
	log.Info("Update request received with payload =", string(payload))

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	err = appInitialize(app, appInfo, path, &payload, nil, UPDATE)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	d, err := db.NewDB(getDBOptions(db.ConfigDB))

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	defer d.DeleteDB()

	keys, err = (*app).translateUpdate(d)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.StartTx(keys, appInfo.tablesToWatch)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	resp, err = (*app).processUpdate(d)

	if err != nil {
		d.AbortTx()
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.CommitTx()

	if err != nil {
		resp.ErrSrc = AppErr
	}

	return resp, err
}

// Replace - Replaces entries in the redis DB pertaining to the path and payload
func Replace(req SetRequest) (SetResponse, error) {
	var err error
	var keys []db.WatchKeys
	var resp SetResponse
	path := req.Path
	payload := req.Payload
	if !isAuthorizedForSet(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Replace Operation",
			Path:   path,
		}
	}

	log.Info("Replace request received with path =", path)
	log.Info("Replace request received with payload =", string(payload))

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	err = appInitialize(app, appInfo, path, &payload, nil, REPLACE)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	d, err := db.NewDB(getDBOptions(db.ConfigDB))

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	defer d.DeleteDB()

	keys, err = (*app).translateReplace(d)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.StartTx(keys, appInfo.tablesToWatch)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	resp, err = (*app).processReplace(d)

	if err != nil {
		d.AbortTx()
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.CommitTx()

	if err != nil {
		resp.ErrSrc = AppErr
	}

	return resp, err
}

// Delete - Deletes entries in the redis DB pertaining to the path
func Delete(req SetRequest) (SetResponse, error) {
	var err error
	var keys []db.WatchKeys
	var resp SetResponse
	path := req.Path
	if !isAuthorizedForSet(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Delete Operation",
			Path:   path,
		}
	}

	log.Info("Delete request received with path =", path)

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	opts := appOptions{deleteEmptyEntry: req.DeleteEmptyEntry}
	err = appInitialize(app, appInfo, path, nil, &opts, DELETE)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	d, err := db.NewDB(getDBOptions(db.ConfigDB))

	if err != nil {
		resp.ErrSrc = ProtoErr
		return resp, err
	}

	defer d.DeleteDB()

	keys, err = (*app).translateDelete(d)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.StartTx(keys, appInfo.tablesToWatch)

	if err != nil {
		resp.ErrSrc = AppErr
		return resp, err
	}

	resp, err = (*app).processDelete(d)

	if err != nil {
		d.AbortTx()
		resp.ErrSrc = AppErr
		return resp, err
	}

	err = d.CommitTx()

	if err != nil {
		resp.ErrSrc = AppErr
	}

	return resp, err
}

// Get - Gets data from the redis DB and converts it to northbound format
func Get(req GetRequest) (GetResponse, error) {
	var payload []byte
	var resp GetResponse
	path := req.Path
	if !isAuthorizedForGet(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Get Operation",
			Path:   path,
		}
	}

	log.Info("Received Get request for path = ", path)

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp = GetResponse{Payload: payload, ErrSrc: ProtoErr}
		return resp, err
	}

	opts := appOptions{depth: req.QueryParams.Depth, content: req.QueryParams.Content, fields: req.QueryParams.Fields, ctxt: req.Ctxt}
	err = appInitialize(app, appInfo, path, nil, &opts, GET)

	if err != nil {
		resp = GetResponse{Payload: payload, ErrSrc: AppErr}
		return resp, err
	}

	dbs, err := getAllDbs(withWriteDisable)

	if err != nil {
		resp = GetResponse{Payload: payload, ErrSrc: ProtoErr}
		return resp, err
	}

	defer closeAllDbs(dbs[:])

	err = (*app).translateGet(dbs)

	if err != nil {
		resp = GetResponse{Payload: payload, ErrSrc: AppErr}
		return resp, err
	}

	resp, err = (*app).processGet(dbs, req.FmtType)

	return resp, err
}

func Action(req ActionRequest) (ActionResponse, error) {
	var payload []byte
	var resp ActionResponse
	path := req.Path

	if !isAuthorizedForAction(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Action Operation",
			Path:   path,
		}
	}

	log.Info("Received Action request for path = ", path)

	app, appInfo, err := getAppModule(path, req.ClientVersion)

	if err != nil {
		resp = ActionResponse{Payload: payload, ErrSrc: ProtoErr}
		return resp, err
	}

	aInfo := *appInfo

	aInfo.isNative = true

	err = appInitialize(app, &aInfo, path, &req.Payload, nil, GET)

	if err != nil {
		resp = ActionResponse{Payload: payload, ErrSrc: AppErr}
		return resp, err
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	dbs, err := getAllDbs()

	if err != nil {
		resp = ActionResponse{Payload: payload, ErrSrc: ProtoErr}
		return resp, err
	}

	defer closeAllDbs(dbs[:])

	err = (*app).translateAction(dbs)

	if err != nil {
		resp = ActionResponse{Payload: payload, ErrSrc: AppErr}
		return resp, err
	}

	resp, err = (*app).processAction(dbs)

	return resp, err
}

// Bulk - BULK Request API for northbounds
// Processes the request in received order
// Transaction based
func Bulk(req BulkRequest) (BulkResponse, error) {
	var err error
	var keys []db.WatchKeys
	var errSrc ErrSource
	var appResp SetResponse

	resp := BulkResponse{}

	if !isAuthorizedForBulk(req) {
		return resp, tlerr.AuthorizationError{
			Format: "User is unauthorized for Action Operation",
		}
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	d, err := db.NewDB(getDBOptions(db.ConfigDB))

	if err != nil {
		return resp, err
	}

	defer d.DeleteDB()

	//Start the transaction without any keys or tables to watch will be added later using AppendWatchTx
	err = d.StartTx(nil, nil)

	if err != nil {
		return resp, err
	}

	for i := range req.Request {
		path := req.Request[i].Entry.Path
		operation := req.Request[i].Operation

		log.Infof("Bulk Request operation: %v received with path = %v", req.Request[i].Operation, path)

		app, appInfo, err := getAppModule(path, req.Request[i].Entry.ClientVersion)
		if err != nil {
			errSrc = ProtoErr
			goto BulkError
		}
		if operation == DELETE {
			opts := appOptions{deleteEmptyEntry: req.Request[i].Entry.DeleteEmptyEntry}
			err = appInitialize(app, appInfo, path, nil, &opts, operation)
		} else {
			payload := req.Request[i].Entry.Payload
			err = appInitialize(app, appInfo, path, &payload, nil, operation)
		}

		if err != nil {
			errSrc = AppErr
			goto BulkError
		}

		switch operation {
		case DELETE:
			keys, err = (*app).translateDelete(d)
			if err != nil && isBulkNotFoundError(err) {
				if !req.Request[i].ResourceCheckOnDelete {
					//GNMI DELETE and YANG-PATCH REMOVE will come here
					log.V(2).Infof("Ignoring Delete error: %+v", err)
					appResp.Err = nil // so that northbounds can ignore
					resp.Response = append(resp.Response, BulkResponseEntry{Operation: req.Request[i].Operation,
						Entry: appResp})
					continue
				}
			}
		case REPLACE:
			keys, err = (*app).translateReplace(d)
		case UPDATE:
			keys, err = (*app).translateUpdate(d)
			if err != nil && isBulkNotFoundError(err) {
				//TODO: Right approach is to invoke CREATE, but REPLACE will solve the purpose as
				//resource does not exists, REPLACE will behave like CREATE.
				//REPLACE is chosen because PATH format and payload is same as UPDATE
				log.V(2).Infof("Since UPDATE Failed, Changing operation type to REPLACE")
				operation = REPLACE
				payload := req.Request[i].Entry.Payload
				err = appInitialize(app, appInfo, path, &payload, nil, operation)
				if err != nil {
					errSrc = AppErr
					goto BulkError
				}
				keys, err = (*app).translateReplace(d)
			}
		case CREATE:
			keys, err = (*app).translateCreate(d)
		default:
			log.Warningf("Unknown operation '%v'", operation)
			err = tlerr.NotSupported("Unknown operation '%v'", operation)
		}

		if err != nil {
			errSrc = AppErr
			goto BulkError
		}

		err = d.AppendWatchTx(keys, appInfo.tablesToWatch)

		if err != nil {
			errSrc = AppErr
			goto BulkError
		}

		switch operation {
		case DELETE:
			appResp, err = (*app).processDelete(d)
			if err != nil && isBulkNotFoundError(err) {
				if !req.Request[i].ResourceCheckOnDelete {
					//GNMI DELETE and YANG-PATCH REMOVE will come here
					log.V(2).Infof("Ignoring Delete error: %+v", err)
					appResp.Err = nil // so that northbounds can ignore
					resp.Response = append(resp.Response, BulkResponseEntry{Operation: req.Request[i].Operation,
						Entry: appResp})
					continue
				}
			}
		case REPLACE:
			appResp, err = (*app).processReplace(d)
		case UPDATE:
			appResp, err = (*app).processUpdate(d)
			if err != nil && isBulkNotFoundError(err) {
				//TODO: Right approach is to invoke CREATE, but REPLACE will solve the purpose as
				//resource does not exists, REPLACE will behave like CREATE.
				//REPLACE is chosen because PATH format and payload is same as UPDATE
				log.V(2).Infof("Since UPDATE Failed, Changing operation type to REPLACE")
				operation = REPLACE
				payload := req.Request[i].Entry.Payload
				err = appInitialize(app, appInfo, path, &payload, nil, operation)
				if err != nil {
					errSrc = AppErr
					goto BulkError
				}
				keys, err = (*app).translateReplace(d)
				if err != nil {
					errSrc = AppErr
					goto BulkError
				}
				err = d.AppendWatchTx(keys, appInfo.tablesToWatch)
				if err != nil {
					errSrc = AppErr
					goto BulkError
				}
				appResp, err = (*app).processReplace(d)
			}
		case CREATE:
			appResp, err = (*app).processCreate(d)
		default:
			log.Warningf("Unknown operation '%v'", operation)
			err = tlerr.NotSupported("Unknown operation '%v'", operation)
		}

		if err != nil {
			errSrc = AppErr
			goto BulkError
		}

		resp.Response = append(resp.Response, BulkResponseEntry{Operation: req.Request[i].Operation, Entry: appResp})

	BulkError:
		if err != nil {
			log.Infof("BulkError: %+v", err)
			d.AbortTx()
			appResp.ErrSrc = errSrc
			appResp.Err = err
			resp.Response = append(resp.Response, BulkResponseEntry{Operation: req.Request[i].Operation,
				Entry: appResp})
			return resp, err
		}
	}

	err = d.CommitTx()

	return resp, err
}

// GetModels - Gets all the models supported by Translib
func GetModels() ([]ModelData, error) {
	var err error

	return getModels(), err
}

// Creates connection will all the redis DBs. To be used for get request
func getAllDbs(opts ...func(*db.Options)) ([db.MaxDB]*db.DB, error) {
	var dbs [db.MaxDB]*db.DB
	var err error
	for dbNum := db.DBNum(0); dbNum < db.MaxDB; dbNum++ {
		if len(dbNum.Name()) == 0 {
			continue
		}
		dbs[dbNum], err = db.NewDB(getDBOptions(dbNum, opts...))
		if err != nil {
			closeAllDbs(dbs[:])
			break
		}
	}

	return dbs, err
}

// Closes the dbs, and nils out the arr.
func closeAllDbs(dbs []*db.DB) {
	for dbsi, d := range dbs {
		if d != nil {
			d.DeleteDB()
			dbs[dbsi] = nil
		}
	}
}

// Compare - Implement Compare method for priority queue for SubscribeResponse struct
func (val SubscribeResponse) Compare(other queue.Item) int {
	o := other.(*SubscribeResponse)
	if val.Timestamp > o.Timestamp {
		return 1
	} else if val.Timestamp == o.Timestamp {
		return 0
	}
	return -1
}

func getDBOptions(dbNo db.DBNum, opts ...func(*db.Options)) db.Options {
	o := db.Options{DBNo: dbNo}
	for _, setopt := range opts {
		setopt(&o)
	}
	return o
}

func withWriteDisable(o *db.Options) {
	o.IsWriteDisabled = true
}

func withOnChange(o *db.Options) {
	o.IsOnChangeEnabled = true
}

func getAppModule(path string, clientVer Version) (*appInterface, *appInfo, error) {
	var app appInterface

	aInfo, err := getAppModuleInfo(path)

	if err != nil {
		return nil, aInfo, err
	}

	if err := validateClientVersion(clientVer, path, aInfo); err != nil {
		return nil, aInfo, err
	}

	app, err = getAppInterface(aInfo.appType)

	if err != nil {
		return nil, aInfo, err
	}

	return &app, aInfo, err
}

func appInitialize(app *appInterface, appInfo *appInfo, path string, payload *[]byte, opts *appOptions, opCode int) error {
	var err error
	var input []byte

	if payload != nil {
		input = *payload
	}

	if appInfo.isNative {
		data := appData{path: path, payload: input}
		data.setOptions(opts)
		(*app).initialize(data)
	} else {
		reqBinder := getRequestBinder(&path, payload, opCode, &(appInfo.ygotRootType))
		ygotStruct, ygotTarget, err := reqBinder.unMarshall()
		if err != nil {
			log.Info("Error in request binding: ", err)
			return err
		}
		data := appData{path: path, payload: input, ygotRoot: ygotStruct, ygotTarget: ygotTarget, ygSchema: reqBinder.targetNodeSchema}
		data.setOptions(opts)
		(*app).initialize(data)
	}

	return err
}

func (data *appData) setOptions(opts *appOptions) {
	if opts != nil {
		data.appOptions = *opts
	}
}
