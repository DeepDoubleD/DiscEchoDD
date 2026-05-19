/**
 * Returns a human-readable label for the current rip-tool sub-phase.
 * Used by drive cards to show what the rip step is actually doing
 * during long quiet phases (redumper REFINE/SPLIT/DVDKEY, MakeMKV
 * pre-rip title enumeration, …).
 */
export function ripSubStepLabel(substep: string | undefined | null): string {
  switch (substep) {
    case 'DUMP':
    case 'read_raw_data':
    case '':
    case undefined:
    case null:
      return 'Read raw data';
    case 'DUMP::EXTRA':
      return 'Reading lead-in / lead-out';
    case 'PROTECTION':
      return 'Checking protection';
    case 'REFINE':
      return 'Recovering damaged sectors (this can take a while)';
    case 'DVDKEY':
      return 'Extracting DVD keys';
    case 'SPLIT':
      return 'Splitting tracks';
    case 'scan':
      return 'Scanning titles (this can take several minutes)';
    default:
      return substep;
  }
}
