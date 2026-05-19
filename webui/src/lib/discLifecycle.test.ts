import { describe, it, expect } from 'vitest';
import { lastEncodingDiscForDrive, activeTranscodeJob } from './discLifecycle';
import type { Disc, Drive, Job } from './wire';

const drive = (id: string): Drive => ({
  id,
  model: 'ASUS',
  bus: 'sr0',
  dev_path: '/dev/sr0',
  state: 'idle',
  last_seen_at: '2026-05-19T10:00:00Z',
  read_offset: 0,
});

const disc = (id: string, lifecycle: Disc['lifecycle_state'], driveID = 'd1'): Disc => ({
  id,
  drive_id: driveID,
  type: 'DVD',
  title: `Disc ${id}`,
  candidates: [],
  created_at: '2026-05-19T09:00:00Z',
  lifecycle_state: lifecycle,
});

const job = (
  id: string,
  discID: string,
  kind: Job['kind'],
  state: Job['state'],
  createdAt: string,
): Job => ({
  id,
  disc_id: discID,
  profile_id: 'p1',
  kind,
  state,
  active_step: 'transcode',
  progress: 0,
  output_bytes: 0,
  started_at: createdAt,
  created_at: createdAt,
  steps: [],
});

describe('lastEncodingDiscForDrive', () => {
  it('returns the encoding disc whose source drive is the given drive', () => {
    const d = drive('d1');
    const discs: Record<string, Disc> = {
      a: disc('a', 'encoding', 'd1'),
      b: disc('b', 'done', 'd1'),
      c: disc('c', 'encoding', 'd2'),
    };
    const jobs: Job[] = [
      job('jA-rip', 'a', 'rip', 'done', '2026-05-19T08:00:00Z'),
      job('jA-tr', 'a', 'transcode', 'running', '2026-05-19T09:00:00Z'),
      job('jB-rip', 'b', 'rip', 'done', '2026-05-19T07:00:00Z'),
      job('jC-rip', 'c', 'rip', 'done', '2026-05-19T08:30:00Z'),
      job('jC-tr', 'c', 'transcode', 'running', '2026-05-19T09:30:00Z'),
    ];
    const got = lastEncodingDiscForDrive(d, discs, jobs);
    expect(got?.id).toBe('a');
  });

  it('returns undefined when no disc on this drive is encoding', () => {
    const d = drive('d1');
    const discs: Record<string, Disc> = {
      a: disc('a', 'done', 'd1'),
    };
    expect(lastEncodingDiscForDrive(d, discs, [])).toBeUndefined();
  });

  it('returns the most recent when multiple discs from one drive are encoding', () => {
    const d = drive('d1');
    const discs: Record<string, Disc> = {
      a: disc('a', 'encoding', 'd1'),
      b: disc('b', 'encoding', 'd1'),
    };
    const jobs: Job[] = [
      job('jA-tr', 'a', 'transcode', 'running', '2026-05-19T09:00:00Z'),
      job('jB-tr', 'b', 'transcode', 'running', '2026-05-19T10:00:00Z'),
    ];
    const got = lastEncodingDiscForDrive(d, discs, jobs);
    expect(got?.id).toBe('b');
  });
});

describe('activeTranscodeJob', () => {
  it('returns the running transcode for a disc', () => {
    const d = disc('a', 'encoding');
    const jobs: Job[] = [
      job('jA-rip', 'a', 'rip', 'done', '2026-05-19T08:00:00Z'),
      job('jA-tr', 'a', 'transcode', 'running', '2026-05-19T09:00:00Z'),
    ];
    const got = activeTranscodeJob(d, jobs);
    expect(got?.id).toBe('jA-tr');
  });

  it('returns the queued transcode when none is running yet', () => {
    const d = disc('a', 'encoding');
    const jobs: Job[] = [job('jA-tr', 'a', 'transcode', 'queued', '2026-05-19T09:00:00Z')];
    const got = activeTranscodeJob(d, jobs);
    expect(got?.id).toBe('jA-tr');
  });

  it('returns undefined when no active transcode exists', () => {
    const d = disc('a', 'done');
    const jobs: Job[] = [job('jA-tr', 'a', 'transcode', 'done', '2026-05-19T09:00:00Z')];
    expect(activeTranscodeJob(d, jobs)).toBeUndefined();
  });
});
