//go:build linux

package purevfs

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	bazilfuse "bazil.org/fuse"
	bazilfs "bazil.org/fuse/fs"
	"golang.org/x/sys/unix"
)

var linuxSystemReadOnlyRoots = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/etc",
}

type linuxExecutionMount struct {
	root     string
	conn     *bazilfuse.Conn
	serveErr chan error
}

type linuxProjectionFS struct {
	root *projectedRoot
}

type linuxProjectionDir struct {
	fs   *linuxProjectionFS
	path string
}

type linuxProjectionFile struct {
	fs   *linuxProjectionFS
	path string
}

type linuxProjectionHandle struct {
	root *projectedRoot
	id   uint64
}

func isWindowsPlatform() bool {
	return false
}

func platformExecutionCapabilities() ExecutionCapabilities {
	if strictExecutionProbe() != nil {
		return ExecutionCapabilities{}
	}
	return StrictBrokerCapabilities()
}

func strictExecutionProbe() error {
	switch {
	case !commandAvailable("bwrap"):
		return fmt.Errorf("%w: bwrap not installed", ErrStrictExecutionUnavailable)
	case !commandAvailable("fusermount3"):
		return fmt.Errorf("%w: fusermount3 not installed", ErrStrictExecutionUnavailable)
	case !linuxSharedMemoryRootAvailable():
		return fmt.Errorf("%w: /dev/shm is not tmpfs", ErrStrictExecutionUnavailable)
	default:
		return nil
	}
}

func mountExecutionRoot(_ context.Context, _ BrokerRunRequest, root *projectedRoot) (mountedExecutionRoot, error) {
	mountpoint, err := newLinuxMountpoint()
	if err != nil {
		return nil, err
	}
	conn, err := bazilfuse.Mount(
		mountpoint,
		bazilfuse.FSName("sylk-purevfs"),
		bazilfuse.Subtype("sylk"),
	)
	if err != nil {
		_ = os.Remove(mountpoint)
		return nil, err
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- bazilfs.Serve(conn, &linuxProjectionFS{root: root})
	}()
	return &linuxExecutionMount{
		root:     mountpoint,
		conn:     conn,
		serveErr: serveErr,
	}, nil
}

func runMountedExecution(ctx context.Context, req BrokerRunRequest, mountRoot string, guard *brokerMemoryGuard) (*BrokerRunResult, error) {
	cmd, err := newLinuxSandboxCommand(ctx, req, mountRoot)
	if err != nil {
		return nil, err
	}
	stdout := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	stderr := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	exitCode, exitErr := captureExitCode(err)
	if exitErr != nil {
		return nil, exitErr
	}
	return &BrokerRunResult{
		ExitCode:        exitCode,
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

func newLinuxMountpoint() (string, error) {
	if err := strictExecutionProbe(); err != nil {
		return "", fmt.Errorf("purevfs: create linux mountpoint: %w", err)
	}
	mountpoint, err := os.MkdirTemp("/dev/shm", "sylk-execfs-*")
	if err != nil {
		return "", fmt.Errorf("purevfs: create linux mountpoint: %w", err)
	}
	return mountpoint, nil
}

func linuxSharedMemoryRootAvailable() bool {
	var stat unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &stat); err != nil {
		return false
	}
	return stat.Type == unix.TMPFS_MAGIC
}

func (m *linuxExecutionMount) Root() string {
	return m.root
}

func (m *linuxExecutionMount) Close() error {
	var firstErr error
	if err := bazilfuse.Unmount(m.root); err != nil && !errors.Is(err, syscall.EINVAL) {
		firstErr = err
	}
	if m.conn != nil {
		if err := m.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	select {
	case err := <-m.serveErr:
		if err != nil && !errors.Is(err, bazilfuse.ErrClosedWithoutInit) && firstErr == nil {
			firstErr = err
		}
	case <-time.After(250 * time.Millisecond):
	}
	if err := os.Remove(m.root); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (f *linuxProjectionFS) Root() (bazilfs.Node, error) {
	return &linuxProjectionDir{fs: f, path: "/"}, nil
}

func (d *linuxProjectionDir) Attr(ctx context.Context, attr *bazilfuse.Attr) error {
	return fillLinuxAttr(ctx, d.fs.root, d.path, attr)
}

func (d *linuxProjectionDir) Lookup(ctx context.Context, name string) (bazilfs.Node, error) {
	child := childExecPath(d.path, name)
	info, err := d.fs.root.Stat(ctx, child)
	if err != nil {
		return nil, err
	}
	return linuxNodeForInfo(d.fs, child, info), nil
}

func (d *linuxProjectionDir) ReadDirAll(ctx context.Context) ([]bazilfuse.Dirent, error) {
	entries, err := d.fs.root.ListDir(ctx, d.path)
	if err != nil {
		return nil, err
	}
	out := make([]bazilfuse.Dirent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, bazilfuse.Dirent{
			Inode: inodeForPath(childExecPath(d.path, entry.Name())),
			Name:  entry.Name(),
			Type:  linuxDirentType(entry),
		})
	}
	return out, nil
}

func (d *linuxProjectionDir) Mkdir(ctx context.Context, req *bazilfuse.MkdirRequest) (bazilfs.Node, error) {
	child := childExecPath(d.path, req.Name)
	if err := d.fs.root.MkdirAll(ctx, child); err != nil {
		return nil, err
	}
	return &linuxProjectionDir{fs: d.fs, path: child}, nil
}

func (d *linuxProjectionDir) Remove(ctx context.Context, req *bazilfuse.RemoveRequest) error {
	return d.fs.root.Delete(ctx, childExecPath(d.path, req.Name))
}

func (d *linuxProjectionDir) Rename(ctx context.Context, req *bazilfuse.RenameRequest, newDir bazilfs.Node) error {
	targetDir, ok := newDir.(*linuxProjectionDir)
	if !ok {
		return syscall.EINVAL
	}
	oldPath := childExecPath(d.path, req.OldName)
	newPath := childExecPath(targetDir.path, req.NewName)
	return d.fs.root.Rename(ctx, oldPath, newPath)
}

func (d *linuxProjectionDir) Create(ctx context.Context, req *bazilfuse.CreateRequest, resp *bazilfuse.CreateResponse) (bazilfs.Node, bazilfs.Handle, error) {
	child := childExecPath(d.path, req.Name)
	id, err := d.fs.root.CreateHandle(ctx, child, true)
	if err != nil {
		return nil, nil, err
	}
	resp.Flags |= bazilfuse.OpenDirectIO
	node := &linuxProjectionFile{fs: d.fs, path: child}
	return node, &linuxProjectionHandle{root: d.fs.root, id: id}, nil
}

func (f *linuxProjectionFile) Attr(ctx context.Context, attr *bazilfuse.Attr) error {
	return fillLinuxAttr(ctx, f.fs.root, f.path, attr)
}

func (f *linuxProjectionFile) Open(ctx context.Context, req *bazilfuse.OpenRequest, resp *bazilfuse.OpenResponse) (bazilfs.Handle, error) {
	id, err := f.fs.root.OpenHandle(ctx, f.path, linuxOpenWritable(req.Flags), req.Flags&bazilfuse.OpenTruncate != 0)
	if err != nil {
		return nil, err
	}
	resp.Flags |= bazilfuse.OpenDirectIO
	return &linuxProjectionHandle{root: f.fs.root, id: id}, nil
}

func (f *linuxProjectionFile) Setattr(ctx context.Context, req *bazilfuse.SetattrRequest, resp *bazilfuse.SetattrResponse) error {
	if !req.Valid.Size() {
		return fillLinuxAttr(ctx, f.fs.root, f.path, &resp.Attr)
	}
	id, err := f.fs.root.OpenHandle(ctx, f.path, true, false)
	if err != nil {
		return err
	}
	defer f.fs.root.ReleaseHandle(ctx, id)
	if err := f.fs.root.TruncateHandle(id, int64(req.Size)); err != nil {
		return err
	}
	return fillLinuxAttr(ctx, f.fs.root, f.path, &resp.Attr)
}

func (h *linuxProjectionHandle) Read(_ context.Context, req *bazilfuse.ReadRequest, resp *bazilfuse.ReadResponse) error {
	data := make([]byte, req.Size)
	n, err := h.root.ReadHandle(h.id, data, req.Offset)
	if err != nil {
		return err
	}
	resp.Data = data[:n]
	return nil
}

func (h *linuxProjectionHandle) Write(_ context.Context, req *bazilfuse.WriteRequest, resp *bazilfuse.WriteResponse) error {
	n, err := h.root.WriteHandle(h.id, req.Data, req.Offset)
	if err != nil {
		return err
	}
	resp.Size = n
	return nil
}

func (h *linuxProjectionHandle) Flush(ctx context.Context, _ *bazilfuse.FlushRequest) error {
	return h.root.FlushHandle(ctx, h.id)
}

func (h *linuxProjectionHandle) Release(ctx context.Context, _ *bazilfuse.ReleaseRequest) error {
	return h.root.ReleaseHandle(ctx, h.id)
}

func fillLinuxAttr(ctx context.Context, root *projectedRoot, name string, attr *bazilfuse.Attr) error {
	info, err := root.Stat(ctx, name)
	if err != nil {
		return err
	}
	attr.Inode = inodeForPath(name)
	attr.Mode = info.Mode()
	attr.Size = uint64(max(info.Size(), 0))
	attr.Mtime = info.ModTime()
	attr.Ctime = info.ModTime()
	return nil
}

func linuxNodeForInfo(fsRoot *linuxProjectionFS, name string, info fs.FileInfo) bazilfs.Node {
	if info.IsDir() {
		return &linuxProjectionDir{fs: fsRoot, path: name}
	}
	return &linuxProjectionFile{fs: fsRoot, path: name}
}

func linuxDirentType(entry fs.DirEntry) bazilfuse.DirentType {
	if entry.IsDir() {
		return bazilfuse.DT_Dir
	}
	return bazilfuse.DT_File
}

func inodeForPath(name string) uint64 {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(normalizeExecPath(name)))
	return sum.Sum64()
}

func childExecPath(parent, name string) string {
	if normalizeExecPath(parent) == "/" {
		return normalizeExecPath("/" + name)
	}
	return normalizeExecPath(path.Join(parent, name))
}

func linuxOpenWritable(flags bazilfuse.OpenFlags) bool {
	return flags.IsWriteOnly() || flags.IsReadWrite()
}

func newLinuxSandboxCommand(ctx context.Context, req BrokerRunRequest, mountRoot string) (*exec.Cmd, error) {
	args := linuxSandboxArgs(req, mountRoot)
	cmd := exec.CommandContext(ctx, "bwrap", args...)
	cmd.Dir = mountRoot
	cmd.Env = linuxExecutionEnv(req.Plan, req.Env)
	return cmd, nil
}

func linuxSandboxArgs(req BrokerRunRequest, mountRoot string) []string {
	args := []string{
		"--unshare-all",
		"--share-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	args = append(args, linuxProjectionBinds(req.Plan, mountRoot)...)
	args = append(args, linuxReadOnlyHostBinds(req.Argv)...)
	if dir := strings.TrimSpace(req.Plan.WorkingDir); dir != "" {
		args = append(args, "--chdir", dir)
	}
	args = append(args, "--")
	return append(args, req.Argv...)
}

func linuxProjectionBinds(plan ExecutionPlan, mountRoot string) []string {
	builder := newLinuxBindBuilder()
	for _, mount := range plan.Mounts {
		if !linuxProjectedMountAvailable(mount) {
			continue
		}
		src := mountedExecPath(mountRoot, mount.VirtualPath)
		dst := normalizeExecPath(mount.VirtualPath)
		builder.add(dst, src, linuxMountReadOnly(mount))
	}
	return builder.args()
}

func linuxReadOnlyHostBinds(argv []string) []string {
	builder := newLinuxBindBuilder()
	for _, root := range linuxSystemReadOnlyRoots {
		builder.add(root, root, true)
	}
	for _, dir := range linuxExtraReadOnlyDirs(argv) {
		builder.add(dir, dir, true)
	}
	return builder.args()
}

func linuxExtraReadOnlyDirs(argv []string) []string {
	seen := make(map[string]struct{})
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			return
		}
		if linuxCoveredBySystemRoot(dir) {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		add(dir)
	}
	if len(argv) > 0 && filepath.IsAbs(argv[0]) {
		add(filepath.Dir(argv[0]))
	}
	slices.Sort(dirs)
	return dirs
}

func linuxCoveredBySystemRoot(name string) bool {
	for _, root := range linuxSystemReadOnlyRoots {
		if name == root || strings.HasPrefix(name, root+"/") {
			return true
		}
	}
	return false
}

func linuxMountReadOnly(mount MountSpec) bool {
	return mount.Access == MountReadOnly
}

func linuxExecutionEnv(plan ExecutionPlan, extra map[string]string) []string {
	env := baseExecutionEnv()
	mergeLinuxExecutionEnv(env, plan.Env)
	mergeLinuxExecutionEnv(env, extra)
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func mergeLinuxExecutionEnv(dst map[string]string, values map[string]string) {
	for key, value := range values {
		if isPathListKey(key) {
			dst[key] = mergeExecutionPathList(dst[key], value)
			continue
		}
		dst[key] = value
	}
}

func linuxProjectedMountAvailable(mount MountSpec) bool {
	if mount.Kind != MountToolchain {
		return true
	}
	path := strings.TrimSpace(mount.BackingPath)
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}

type linuxBindBuilder struct {
	dirs  map[string]struct{}
	binds map[string]linuxBind
}

type linuxBind struct {
	src      string
	readOnly bool
}

func newLinuxBindBuilder() *linuxBindBuilder {
	return &linuxBindBuilder{
		dirs:  make(map[string]struct{}),
		binds: make(map[string]linuxBind),
	}
}

func (b *linuxBindBuilder) add(dst, src string, readOnly bool) {
	dst = normalizeExecPath(dst)
	if dst == "/" {
		return
	}
	for _, dir := range linuxParentDirs(dst) {
		b.dirs[dir] = struct{}{}
	}
	b.binds[dst] = linuxBind{src: src, readOnly: readOnly}
}

func (b *linuxBindBuilder) args() []string {
	var out []string
	dirs := make([]string, 0, len(b.dirs))
	for dir := range b.dirs {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		out = append(out, "--dir", dir)
	}
	dsts := make([]string, 0, len(b.binds))
	for dst := range b.binds {
		dsts = append(dsts, dst)
	}
	slices.Sort(dsts)
	for _, dst := range dsts {
		bind := b.binds[dst]
		flag := "--bind"
		if bind.readOnly {
			flag = "--ro-bind"
		}
		out = append(out, flag, bind.src, dst)
	}
	return out
}

func linuxParentDirs(name string) []string {
	current := normalizeExecPath(path.Dir(name))
	if current == "/" {
		return nil
	}
	var dirs []string
	for current != "/" {
		dirs = append(dirs, current)
		current = normalizeExecPath(path.Dir(current))
	}
	slices.Reverse(dirs)
	return dirs
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
