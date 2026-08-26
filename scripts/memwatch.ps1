param(
    [Parameter(Mandatory=$true)][int]$TargetPid,
    [int]$IntervalMs = 500
)

$peak = 0.0
$start = Get-Date
Write-Host ("# memwatch: target PID={0}, every {1}ms`n# t(s)  RSS_MB  Peak_MB" -f $TargetPid, $IntervalMs)
try {
    while ($true) {
        $p = Get-Process -Id $TargetPid -ErrorAction SilentlyContinue
        if (-not $p) {
            Write-Host "# process $TargetPid exited"
            break
        }
        $mb = [math]::Round($p.WS / 1MB, 2)
        if ($mb -gt $peak) { $peak = $mb }
        $elapsed = [math]::Round(((Get-Date) - $start).TotalSeconds, 1)
        Write-Host ("{0,6}  {1,7}  {2,8}" -f $elapsed, $mb, $peak)
        Start-Sleep -Milliseconds $IntervalMs
    }
} finally {
    Write-Host ("`n# PEAK RSS_MB={0}" -f $peak)
}
