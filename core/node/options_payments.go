/*
 * Copyright (C) 2019 The "MysteriumNetwork/node" Authors.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package node

import "time"

// OptionsPayments controls the behaviour of payments
type OptionsPayments struct {
	MaxAllowedPaymentPercentile        int
	BCTimeout                          time.Duration
	AccountantPromiseSettlingThreshold float64
	SettlementTimeout                  time.Duration
	MystSCAddress                      string
	ConsumerUpperGBPriceBound          uint64
	ConsumerLowerGBPriceBound          uint64
	ConsumerUpperMinutePriceBound      uint64
	ConsumerLowerMinutePriceBound      uint64
	ConsumerDataLeewayMegabytes        uint64
	ProviderInvoiceFrequency           time.Duration
	MaxUnpaidInvoiceValue              uint64
}
