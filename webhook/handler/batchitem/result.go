// Copyright 2025 The Podseidon Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package batchitem

type Result uint8

const (
	ResultNewDisruption = Result(iota + 1)
	ResultNoPpr
	ResultAlreadyUnhealthy
	ResultAlreadyHasBucket
	ResultNeedRetry
	ResultRejected
)

func (result Result) String() string {
	switch result {
	case ResultNewDisruption:
		return "NewDisruption"
	case ResultNoPpr:
		return "NoPpr"
	case ResultAlreadyUnhealthy:
		return "AlreadyUnhealthy"
	case ResultAlreadyHasBucket:
		return "AlreadyHasBucket"
	case ResultNeedRetry:
		return "NeedRetry"
	case ResultRejected:
		return "Rejected"
	default:
		return "Unknown"
	}
}
