// DiscEcho's thin CLI wrapper around the vendored Ps3DiscDumper library.
// Not part of upstream 13xforever/ps3-disc-dumper (which ships only an
// Avalonia GUI) -- this exists so the Go daemon can shell out to it and
// parse stdout, matching how it already drives redumper/HandBrake/
// MakeMKV. See daemon/tools/ps3dumper.go for the Go-side caller.
//
// Output contract (stdout): every line the Go side actually parses is
// prefixed and JSON-encoded on a single line -- everything else on
// stdout (the vendored library's own Log.* calls) is passed through
// for the job log but never parsed:
//   PS3DUMPER_PROGRESS: {"processed_sectors":N,"total_sectors":N,"current_file":N,"total_files":N}
//   PS3DUMPER_RESULT: {"success":true,"product_code":"...","title":"...","total_bytes":N,"total_files":N}
//   PS3DUMPER_RESULT: {"success":false,"error":"..."}
//
// Subcommands:
//   detect <mountpoint>
//     Runs DetectDisc only -- no key lookup, no dump. This is what
//     Identify() calls pre-rip: PS3 discs are stock-mountable (the
//     encryption is per-file content, not a drive-level read lockout
//     the way Wii/GameCube's non-standard format is), so ProductCode
//     is available without ever needing a disc key.
//   dump <mountpoint> <outputDir> <keyCacheDir>
//     Full DetectDisc -> FindDiscKeyAsync -> DumpAsync.

using System.Text.Json;
using Ps3DiscDumper;

if (args.Length == 0)
{
    Console.Error.WriteLine("usage: ps3dumper-cli detect <mountpoint>");
    Console.Error.WriteLine("       ps3dumper-cli dump <mountpoint> <outputDir> <keyCacheDir>");
    return 2;
}

switch (args[0])
{
    case "detect" when args.Length >= 2:
        return await RunDetect(args[1]);
    case "dump" when args.Length >= 4:
        return await RunDump(args[1], args[2], args[3]);
    default:
        Console.Error.WriteLine($"unrecognized invocation: {string.Join(' ', args)}");
        return 2;
}

static async Task<int> RunDetect(string mountpoint)
{
    using var dumper = new Dumper();
    try
    {
        // ProductCode-only naming: the final library path/name is
        // DiscEcho's job (RenderOutputPath against the profile's
        // OutputPathTemplate, after post-rip Redump lookup), not this
        // tool's -- OutputDir here is only ever used as a spool
        // subfolder name during `dump`, never surfaced to the user.
        dumper.DetectDisc(mountpoint, d => d.ProductCode ?? "unknown");
        WriteResult(new
        {
            success = true,
            product_code = dumper.ProductCode,
            title = dumper.Title,
            total_bytes = dumper.TotalFileSize,
            total_files = dumper.TotalFileCount,
        });
        return 0;
    }
    catch (Exception ex)
    {
        WriteResult(new { success = false, error = ex.Message });
        return 1;
    }
}

static async Task<int> RunDump(string mountpoint, string outputDir, string keyCacheDir)
{
    using var dumper = new Dumper();
    using var progressTimer = new PeriodicTimer(TimeSpan.FromSeconds(2));
    var progressTask = Task.Run(async () =>
    {
        try
        {
            while (await progressTimer.WaitForNextTickAsync().ConfigureAwait(false))
            {
                WriteProgress(new
                {
                    processed_sectors = dumper.ProcessedSectors,
                    total_sectors = dumper.TotalSectors,
                    current_file = dumper.CurrentFileNumber,
                    total_files = dumper.TotalFileCount,
                });
            }
        }
        catch (OperationCanceledException) { /* progressTimer disposed on completion */ }
    });

    try
    {
        Directory.CreateDirectory(outputDir);
        Directory.CreateDirectory(keyCacheDir);

        dumper.DetectDisc(mountpoint, d => d.ProductCode ?? "unknown");
        await dumper.FindDiscKeyAsync(keyCacheDir).ConfigureAwait(false);
        await dumper.DumpAsync(outputDir).ConfigureAwait(false);

        progressTimer.Dispose();
        try { await progressTask.ConfigureAwait(false); } catch { /* already stopped */ }

        var brokenCount = dumper.BrokenFiles.Count;
        WriteResult(new
        {
            success = brokenCount == 0,
            product_code = dumper.ProductCode,
            title = dumper.Title,
            total_bytes = dumper.TotalFileSize,
            total_files = dumper.TotalFileCount,
            broken_files = brokenCount,
            output_subdir = dumper.OutputDir,
        });
        return brokenCount == 0 ? 0 : 1;
    }
    catch (Exception ex)
    {
        progressTimer.Dispose();
        try { await progressTask.ConfigureAwait(false); } catch { /* already stopped */ }
        WriteResult(new { success = false, error = ex.Message });
        return 1;
    }
}

static void WriteProgress<T>(T payload) =>
    Console.WriteLine("PS3DUMPER_PROGRESS: " + JsonSerializer.Serialize(payload));

static void WriteResult<T>(T payload) =>
    Console.WriteLine("PS3DUMPER_RESULT: " + JsonSerializer.Serialize(payload));
