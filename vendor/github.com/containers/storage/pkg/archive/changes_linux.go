package archive

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"unsafe"

	"github.com/containers/storage/pkg/system"
	"golang.org/x/sys/unix"
)

// walker is used to implement collectFileInfoForChanges on linux. Where this
// method in general returns the entire contents of two directory trees, we
// optimize some FS calls out on linux. In particular, we take advantage of the
// fact that getdents(2) returns the inode of each file in the directory being
// walked, which, when walking two trees in parallel to generate a list of
// changes, can be used to prune subtrees without ever having to lstat(2) them
// directly. Eliminating stat calls in this way can save up to seconds on large
// images.
type walker struct {
	dir1  string
	dir2  string
	root1 *FileInfo
	root2 *FileInfo
}

// collectFileInfoForChanges returns a complete representation of the trees
// rooted at dir1 and dir2, with one important exception: any subtree or
// leaf where the inode and device numbers are an exact match between dir1
// and dir2 will be pruned from the results. This method is *only* to be used
// to generating a list of changes between the two directories, as it does not
// reflect the full contents.
func collectFileInfoForChanges(dir1, dir2 string) (*FileInfo, *FileInfo, error) {
	w := &walker{
		dir1:  dir1,
		dir2:  dir2,
		root1: newRootFileInfo(),
		root2: newRootFileInfo(),
	}

	i1, err := os.Lstat(w.dir1)
	if err != nil {
		return nil, nil, err
	}
	i2, err := os.Lstat(w.dir2)
	if err != nil {
		return nil, nil, err
	}

	if err := w.walk("/", i1, i2); err != nil {
		return nil, nil, err
	}

	return w.root1, w.root2, nil
}

// Given a FileInfo, its path info, and a reference to the root of the tree
// being constructed, register this file with the tree.
func walkchunk(path string, fi os.FileInfo, dir string, root *FileInfo) error {
	if fi == nil {
		return nil
	}
	parent := root.LookUp(filepath.Dir(path))
	if parent == nil {
		return fmt.Errorf("walkchunk: Unexpectedly no parent for %s", path)
	}
	info := &FileInfo{
		name:     filepath.Base(path),
		children: make(map[string]*FileInfo),
		parent:   parent,
	}
	cpath := filepath.Join(dir, path)
	stat, err := system.FromStatT(fi.Sys().(*syscall.Stat_t))
	if err != nil {
		return err
	}
	info.stat = stat
	info.capability, _ = system.Lgetxattr(cpath, "security.capability") // lgetxattr(2): fs access
	parent.children[info.name] = info
	return nil
}

// Walk a subtree rooted at the same path in both trees being iterated. For
// example, /docker/overlay/1234/a/b/c/d and /docker/overlay/8888/a/b/c/d
func (w *walker) walk(path string, i1, i2 os.FileInfo) (err error) {
	// Register these nodes with the return trees, unless we're still at the
	// (already-created) roots:
	if path != "/" {
		if err := walkchunk(path, i1, w.dir1, w.root1); err != nil {
			return err
		}
		if err := walkchunk(path, i2, w.dir2, w.root2); err != nil {
			return err
		}
	}

	is1Dir := i1 != nil && i1.IsDir()
	is2Dir := i2 != nil && i2.IsDir()

	sameDevice := false
	if i1 != nil && i2 != nil {
		si1 := i1.Sys().(*syscall.Stat_t)
		si2 := i2.Sys().(*syscall.Stat_t)
		if si1.Dev == si2.Dev {
			sameDevice = true
		}
	}

	// If these files are both non-existent, or leaves (non-dirs), we are done.
	if !is1Dir && !is2Dir {
		return nil
	}

	// Fetch the names of all the files contained in both directories being walked:
	var names1, names2 []nameIno
	if is1Dir {
		names1, err = readdirnames(filepath.Join(w.dir1, path)) // getdents(2): fs access
		if err != nil {
			return err
		}
	}
	if is2Dir {
		names2, err = readdirnames(filepath.Join(w.dir2, path)) // getdents(2): fs access
		if err != nil {
			return err
		}
	}

	// We have lists of the files contained in both parallel directories, sorted
	// in the same order. Walk them in parallel, generating a unique merged list
	// of all items present in either or both directories.
	var names []string
	ix1 := 0
	ix2 := 0

	for {
		if ix1 >= len(names1) {
			break
		}
		if ix2 >= len(names2) {
			break
		}

		ni1 := names1[ix1]
		ni2 := names2[ix2]

		switch bytes.Compare([]byte(ni1.name), []byte(ni2.name)) {
		case -1: // ni1 < ni2 -- advance ni1
			// we will not encounter ni1 in names2
			names = append(names, ni1.name)
			ix1++
		case 0: // ni1 == ni2
			if ni1.ino != ni2.ino || !sameDevice {
				names = append(names, ni1.name)
			}
			ix1++
			ix2++
		case 1: // ni1 > ni2 -- advance ni2
			// we will not encounter ni2 in names1
			names = append(names, ni2.name)
			ix2++
		}
	}
	for ix1 < len(names1) {
		names = append(names, names1[ix1].name)
		ix1++
	}
	for ix2 < len(names2) {
		names = append(names, names2[ix2].name)
		ix2++
	}

	// For each of the names present in either or both of the directories being
	// iterated, stat the name under each root, and recurse the pair of them:
	for _, name := range names {
		fname := filepath.Join(path, name)
		var cInfo1, cInfo2 os.FileInfo
		if is1Dir {
			cInfo1, err = os.Lstat(filepath.Join(w.dir1, fname)) // lstat(2): fs access
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if is2Dir {
			cInfo2, err = os.Lstat(filepath.Join(w.dir2, fname)) // lstat(2): fs access
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err = w.walk(fname, cInfo1, cInfo2); err != nil {
			return err
		}
	}
	return nil
}

// {name,inode} pairs used to support the early-pruning logic of the walker type
type nameIno struct {
	name string
	ino  uint64
}

type nameInoSlice []nameIno

func (s nameInoSlice) Len() int           { return len(s) }
func (s nameInoSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s nameInoSlice) Less(i, j int) bool { return s[i].name < s[j].name }

// readdirnames is a hacked-apart version of the Go stdlib code, exposing inode
// numbers further up the stack when reading directory contents. Unlike
// os.Readdirnames, which returns a list of filenames, this function returns a
// list of {filename,inode} pairs.
func readdirnames(dirname string) (names []nameIno, err error) {
	var (
		size = 100
		buf  = make([]byte, 4096)
		nbuf int
		bufp int
		nb   int
	)

	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	names = make([]nameIno, 0, size) // Empty with room to grow.
	for {
		// Refill the buffer if necessary
		if bufp >= nbuf {
			bufp = 0
			nbuf, err = unix.ReadDirent(int(f.Fd()), buf) // getdents on linux
			if nbuf < 0 {
				nbuf = 0
			}
			if err != nil {
				return nil, os.NewSyscallError("readdirent", err)
			}
			if nbuf <= 0 {
				break // EOF
			}
		}

		// Drain the buffer
		nb, names = parseDirent(buf[bufp:nbuf], names)
		bufp += nb
	}

	sl := nameInoSlice(names)
	sort.Sort(sl)
	return sl, nil
}

// parseDirent is a minor modification of unix.ParseDirent (linux version)
// which returns {name,inode} pairs instead of just names.
func parseDirent(buf []byte, names []nameIno) (consumed int, newnames []nameIno) {
	origlen := len(buf)
	for len(buf) > 0 {
		dirent := (*unix.Dirent)(unsafe.Pointer(&buf[0]))
		buf = buf[dirent.Reclen:]
		if dirent.Ino == 0 { // File absent in directory.
			continue
		}
		bytes := (*[10000]byte)(unsafe.Pointer(&dirent.Name[0]))
		var name = string(bytes[0:clen(bytes[:])])
		if name == "." || name == ".." { // Useless names
			continue
		}
		names = append(names, nameIno{name, dirent.Ino})
	}
	return origlen - len(buf), names
}

func clen(n []byte) int {
	for i := 0; i < len(n); i++ {
		if n[i] == 0 {
			return i
		}
	}
	return len(n)
}

// OverlayChanges walks the path rw and determines changes for the files in the path,
// with respect to the parent layers
func OverlayChanges(layers []string, rw string) ([]Change, error) {
	dc := func(root, path string, fi os.FileInfo) (string, error) {
		return overlayDeletedFile(layers, root, path, fi)
	}
	return changes(layers, rw, dc, nil, overlayLowerContainsWhiteout)
}

func overlayLowerContainsWhiteout(root, path string) (bool, error) {
	// Whiteout for a file or directory has the same name, but is for a character
	// device with major/minor of 0/0.
	stat, err := os.Stat(filepath.Join(root, path))
	if err != nil && !os.IsNotExist(err) && !isENOTDIR(err) {
		// Not sure what happened here.
		return false, err
	}
	if err == nil && stat.Mode()&os.ModeCharDevice != 0 {
		// Check if there's whiteout for the specified item in the specified layer.
		s := stat.Sys().(*syscall.Stat_t)
		if major(s.Rdev) == 0 && minor(s.Rdev) == 0 {
			return true, nil
		}
	}
	return false, nil
}

func overlayDeletedFile(layers []string, root, path string, fi os.FileInfo) (string, error) {
	// If it's a whiteout item, then a file or directory with that name is removed by this layer.
	if fi.Mode()&os.ModeCharDevice != 0 {
		s := fi.Sys().(*syscall.Stat_t)
		if major(s.Rdev) == 0 && minor(s.Rdev) == 0 {
			return path, nil
		}
	}
	// After this we only need to pay attention to directories.
	if !fi.IsDir() {
		return "", nil
	}
	// If the directory isn't marked as opaque, then it's just a normal directory.
	opaque, err := system.Lgetxattr(filepath.Join(root, path), "trusted.overlay.opaque")
	if err != nil {
		return "", err
	}
	if len(opaque) != 1 || opaque[0] != 'y' {
		return "", err
	}
	// If there are no lower layers, then it can't have been deleted and recreated in this layer.
	if len(layers) == 0 {
		return "", err
	}
	// At this point, we have a directory that's opaque.  If it appears in one of the lower
	// layers, then it was newly-created here, so it wasn't also deleted here.
	for _, layer := range layers {
		stat, err := os.Stat(filepath.Join(layer, path))
		if err != nil && !os.IsNotExist(err) && !isENOTDIR(err) {
			// Not sure what happened here.
			return "", err
		}
		if err == nil {
			if stat.Mode()&os.ModeCharDevice != 0 {
				// It's a whiteout for this directory, so it can't have been
				// deleted in this layer.
				s := stat.Sys().(*syscall.Stat_t)
				if major(s.Rdev) == 0 && minor(s.Rdev) == 0 {
					return "", nil
				}
			}
			// It's not whiteout, so it was there in the older layer, so it has to be
			// marked as deleted in this layer.
			return path, nil
		}
		for dir := filepath.Dir(path); dir != "" && dir != string(os.PathSeparator); dir = filepath.Dir(dir) {
			// Check for whiteout for a parent directory.
			stat, err := os.Stat(filepath.Join(layer, dir))
			if err != nil && !os.IsNotExist(err) && !isENOTDIR(err) {
				// Not sure what happened here.
				return "", err
			}
			if err == nil {
				if stat.Mode()&os.ModeCharDevice != 0 {
					// If it's whiteout for a parent directory, then the
					// original directory wasn't inherited into the top layer.
					s := stat.Sys().(*syscall.Stat_t)
					if major(s.Rdev) == 0 && minor(s.Rdev) == 0 {
						return "", nil
					}
				}
			}
		}
	}

	// We didn't find the same path in any older layers, so it was new in this one.
	return "", nil

}
