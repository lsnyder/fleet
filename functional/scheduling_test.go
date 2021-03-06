/*
   Copyright 2014 CoreOS, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package functional

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coreos/fleet/functional/platform"
	"github.com/coreos/fleet/functional/util"
)

// Start three pairs of services, asserting each pair land on the same
// machine due to the MachineOf options in the unit files.
func TestScheduleMachineOf(t *testing.T) {
	cluster, err := platform.NewNspawnCluster("smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Destroy()

	// Start with a simple three-node cluster
	if err := platform.CreateNClusterMembers(cluster, 3, platform.MachineConfig{}); err != nil {
		t.Fatal(err)
	}
	machines, err := cluster.WaitForNMachines(3)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure we can SSH into each machine using fleetctl
	for _, machine := range machines {
		if stdout, stderr, err := cluster.Fleetctl("--strict-host-key-checking=false", "ssh", machine, "uptime"); err != nil {
			t.Errorf("Unable to SSH into fleet machine: \nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
		}
	}

	// Start the 3 pairs of services
	for i := 0; i < 3; i++ {
		ping := fmt.Sprintf("fixtures/units/ping.%d.service", i)
		pong := fmt.Sprintf("fixtures/units/pong.%d.service", i)
		_, _, err := cluster.Fleetctl("start", "--no-block", ping, pong)
		if err != nil {
			t.Errorf("Failed starting units: %v", err)
		}
	}

	// All 6 services should be visible immediately and become ACTIVE
	// shortly thereafter
	stdout, _, err := cluster.Fleetctl("list-unit-files", "--no-legend")
	if err != nil {
		t.Fatalf("Failed to run list-unit-files: %v", err)
	}
	units := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(units) != 6 {
		t.Fatalf("Did not find six units in cluster: \n%s", stdout)
	}
	active, err := cluster.WaitForNActiveUnits(6)
	if err != nil {
		t.Fatal(err)
	}
	states, err := util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		ping := fmt.Sprintf("ping.%d.service", i)
		pingState, ok := states[ping]
		if !ok {
			t.Errorf("Failed to find state for %s", ping)
			continue
		}

		pong := fmt.Sprintf("pong.%d.service", i)
		pongState, ok := states[pong]
		if !ok {
			t.Errorf("Failed to find state for %s", pong)
			continue
		}

		if len(pingState.Machine) == 0 {
			t.Errorf("Unit %s is not reporting machine", ping)
		}

		if len(pongState.Machine) == 0 {
			t.Errorf("Unit %s is not reporting machine", pong)
		}

		if pingState.Machine != pongState.Machine {
			t.Errorf("Units %s and %s are not on same machine", ping, pong)
		}
	}

	// Ensure a pair of units migrate together when their host goes down
	mach := states["ping.1.service"].Machine
	if _, _, err = cluster.Fleetctl("--strict-host-key-checking=false", "ssh", mach, "sudo", "systemctl", "stop", "fleet"); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.WaitForNMachines(2); err != nil {
		t.Fatal(err)
	}
	active, err = cluster.WaitForNActiveUnits(6)
	if err != nil {
		t.Fatal(err)
	}
	states, err = util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	newPingMach := states["ping.1.service"].Machine
	if mach == newPingMach {
		t.Fatalf("Unit ping.1.service did not appear to migrate")
	}

	newPongMach := states["pong.1.service"].Machine
	if newPingMach != newPongMach {
		t.Errorf("Unit pong.1.service did not migrate with ping.1.service")
	}
}

// Start 5 services that conflict with one another. Assert that only
// 3 of the 5 are started.
func TestScheduleConflicts(t *testing.T) {
	cluster, err := platform.NewNspawnCluster("smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Destroy()

	// Start with a simple three-node cluster
	if err := platform.CreateNClusterMembers(cluster, 3, platform.MachineConfig{}); err != nil {
		t.Fatal(err)
	}
	machines, err := cluster.WaitForNMachines(3)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure we can SSH into each machine using fleetctl
	for _, machine := range machines {
		if stdout, stderr, err := cluster.Fleetctl("--strict-host-key-checking=false", "ssh", machine, "uptime"); err != nil {
			t.Errorf("Unable to SSH into fleet machine: \nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
		}
	}

	for i := 0; i < 5; i++ {
		unit := fmt.Sprintf("fixtures/units/conflict.%d.service", i)
		_, _, err := cluster.Fleetctl("start", "--no-block", unit)
		if err != nil {
			t.Errorf("Failed starting unit %s: %v", unit, err)
		}
	}

	// All 5 services should be visible immediately and 3 should become
	// ACTIVE shortly thereafter
	stdout, _, err := cluster.Fleetctl("list-unit-files", "--no-legend")
	if err != nil {
		t.Fatalf("Failed to run list-unit-files: %v", err)
	}
	units := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(units) != 5 {
		t.Fatalf("Did not find five units in cluster: \n%s", stdout)
	}
	active, err := cluster.WaitForNActiveUnits(3)
	if err != nil {
		t.Fatal(err)
	}
	states, err := util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	machineSet := make(map[string]bool)

	for unit, unitState := range states {
		if len(unitState.Machine) == 0 {
			t.Errorf("Unit %s is not reporting machine", unit)
		}

		machineSet[unitState.Machine] = true
	}

	if len(machineSet) != 3 {
		t.Errorf("3 active units not running on 3 unique machines")
	}
}

func TestScheduleOneWayConflict(t *testing.T) {
	cluster, err := platform.NewNspawnCluster("smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Destroy()

	// Start with a simple three-node cluster
	if err := platform.CreateNClusterMembers(cluster, 1, platform.MachineConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.WaitForNMachines(1); err != nil {
		t.Fatal(err)
	}

	// Start a unit that conflicts with a yet-to-be-scheduled unit
	name := "fixtures/units/conflicts-with-hello.service"
	if _, _, err := cluster.Fleetctl("start", "--no-block", name); err != nil {
		t.Fatalf("Failed starting unit %s: %v", name, err)
	}

	active, err := cluster.WaitForNActiveUnits(1)
	if err != nil {
		t.Fatal(err)
	}
	states, err := util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	// Start a unit that has not defined conflicts
	name = "fixtures/units/hello.service"
	cluster.Fleetctl("start", "--no-block", name)

	// Both units should show up, but only conflicts-with-hello.service
	// should report ACTIVE
	stdout, _, err := cluster.Fleetctl("list-unit-files", "--no-legend")
	if err != nil {
		t.Fatalf("Failed to run list-unit-files: %v", err)
	}
	units := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(units) != 2 {
		t.Fatalf("Did not find two units in cluster: \n%s", stdout)
	}
	active, err = cluster.WaitForNActiveUnits(1)
	if err != nil {
		t.Fatal(err)
	}
	states, err = util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	for unit := range states {
		if unit != "conflicts-with-hello.service" {
			t.Error("Incorrect unit started:", unit)
		}
	}

	// Destroying the conflicting unit should allow the other to start
	name = "conflicts-with-hello.service"
	if _, _, err := cluster.Fleetctl("destroy", name); err != nil {
		t.Fatalf("Failed destroying %s", name)
	}
	// TODO(jonboulle): fix this race. Since we no longer immediately
	// remove unit state on unit destruction (and instead wait for
	// UnitStateGenerator/UnitStatePublisher to clean up), the old unit
	// shows up as active for quite some time.
	time.Sleep(5 * time.Second)
	stdout, _, err = cluster.Fleetctl("list-units", "--no-legend")
	if err != nil {
		t.Fatalf("Failed to run list-units: %v", err)
	}
	units = strings.Split(strings.TrimSpace(stdout), "\n")
	if len(units) != 1 {
		t.Fatalf("Did not find one unit in cluster: \n%s", stdout)
	}
	active, err = cluster.WaitForNActiveUnits(1)
	if err != nil {
		t.Fatal(err)
	}
	states, err = util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}
	for unit := range states {
		if unit != "hello.service" {
			t.Error("Incorrect unit started:", unit)
		}
	}

}

// Ensure units can be scheduled directly to a given machine using the
// MachineID unit option.
func TestScheduleConditionMachineID(t *testing.T) {
	cluster, err := platform.NewNspawnCluster("smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Destroy()

	// Start with a simple three-node cluster
	if err := platform.CreateNClusterMembers(cluster, 3, platform.MachineConfig{}); err != nil {
		t.Fatal(err)
	}
	machines, err := cluster.WaitForNMachines(3)
	if err != nil {
		t.Fatal(err)
	}

	// Start 3 units that are each scheduled to one of our machines
	schedule := make(map[string]string)
	for _, machine := range machines {
		contents := `
[Service]
ExecStart=/bin/bash -c "while true; do echo Hello, World!; sleep 1; done"

[X-Fleet]
MachineID=%s
`
		unitFile, err := util.TempUnit(fmt.Sprintf(contents, machine))
		if err != nil {
			t.Fatalf("Failed creating temporary unit: %v", err)
		}
		defer os.Remove(unitFile)

		_, _, err = cluster.Fleetctl("start", unitFile)
		if err != nil {
			t.Fatalf("Failed starting unit file %s: %v", unitFile, err)
		}

		unit := filepath.Base(unitFile)
		schedule[unit] = machine
	}

	// Block until our three units have been started
	active, err := cluster.WaitForNActiveUnits(3)
	if err != nil {
		t.Fatal(err)
	}
	states, err := util.ActiveToSingleStates(active)
	if err != nil {
		t.Fatal(err)
	}

	for unit, unitState := range states {
		if unitState.Machine != schedule[unit] {
			t.Errorf("Unit %s was scheduled to %s, expected %s", unit, unitState.Machine, schedule[unit])
		}
	}
}

func TestScheduleGlobalUnits(t *testing.T) {
	// Create a three-member cluster
	cluster, err := platform.NewNspawnCluster("smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Destroy()
	if err := platform.CreateNClusterMembers(cluster, 3, platform.MachineConfig{}); err != nil {
		t.Fatal(err)
	}
	machines, err := cluster.WaitForNMachines(3)
	if err != nil {
		t.Fatal(err)
	}

	// Launch a couple of simple units
	cluster.Fleetctl("start", "--no-block", "fixtures/units/hello.service", "fixtures/units/goodbye.service")

	// Both units should show up active
	_, err = cluster.WaitForNActiveUnits(2)
	if err != nil {
		t.Fatal(err)
	}

	// Now add a global unit
	cluster.Fleetctl("start", "--no-block", "fixtures/units/global.service")

	// Should see 2 + 3 units
	states, err := cluster.WaitForNActiveUnits(5)
	if err != nil {
		t.Fatal(err)
	}

	// Each machine should have a single global unit
	us := states["global.service"]
	for _, mach := range machines {
		var found bool
		for _, state := range us {
			if state.Machine == mach {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Did not find global unit on machine %v", mach)
			t.Logf("Found unit states:")
			for _, state := range states {
				t.Logf("%#v", state)
			}
		}
	}
}
