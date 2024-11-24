// Copyright 2024 The Podseidon Authors.
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

package util

import "golang.org/x/exp/constraints"

// Returns the nearest integer to {numerator / divisor} with half-positive rounding.
func RoundedIntDiv[T constraints.Integer](numerator, divisor T) T {
	if divisor < 0 {
		divisor = -divisor
	}

	quotient := numerator / divisor
	remainder := numerator % divisor

	if numerator > 0 && remainder*2 >= divisor {
		quotient++
	}

	if numerator < 0 && (divisor+remainder)*2 < divisor {
		quotient--
	}

	return quotient
}
