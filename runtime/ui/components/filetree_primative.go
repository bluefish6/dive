package components

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/sirupsen/logrus"
	"github.com/wagoodman/dive/dive/filetree"
	"github.com/wagoodman/dive/runtime/ui/format"
)

// TODO simplify this interface.
type TreeModel interface {
	StringBetween(int, int, bool) string
	VisitDepthParentFirst(filetree.Visitor, filetree.VisitEvaluator) error
	VisitDepthChildFirst(filetree.Visitor, filetree.VisitEvaluator) error
	RemovePath(path string) error
	VisibleSize() int
	SetLayerIndex(int) bool
	ToggleHiddenFileType(filetype filetree.DiffType) bool
}

type TreeView struct {
	*tview.Box
	tree TreeModel

	// Note that the following two fields are distinct
	// treeIndex is the index about where we are in the current fileTree
	// this should be updated every keypress
	treeIndex int

	bufferIndexLowerBound int

	globalCollapseAll bool

	inputHandler func(event *tcell.EventKey, setFocus func(p tview.Primitive))

	keyBindings map[string]KeyBinding

	showAttributes bool
}

func NewTreeView(tree TreeModel) *TreeView {
	return &TreeView{
		Box:               tview.NewBox(),
		tree:              tree,
		globalCollapseAll: true,
		showAttributes:    true,
		inputHandler:      nil,
	}
}

type KeyBindingConfig interface {
	GetKeyBinding(key string) (KeyBinding, error)
}

// Implementation notes:
// need to set up our input handler here,
// Should probably factor out keybinding initialization into a new function
//
func (t *TreeView) Setup(config KeyBindingConfig) *TreeView {
	t.tree.SetLayerIndex(0)

	bindingSettings := map[string]keyAction{
		"keybinding.toggle-collapse-dir":        t.collapseDir,
		"keybinding.toggle-collapse-all-dir":    t.collapseOrExpandAll,
		"keybinding.toggle-filetree-attributes": func() bool { t.showAttributes = !t.showAttributes; return true },
		"keybinding.toggle-added-files":         func() bool { t.tree.ToggleHiddenFileType(filetree.Added); return false },
		"keybinding.toggle-removed-files":       func() bool { return t.tree.ToggleHiddenFileType(filetree.Removed) },
		"keybinding.toggle-modified-files":      func() bool { return t.tree.ToggleHiddenFileType(filetree.Modified) },
		"keybinding.toggle-unmodified-files":    func() bool { return t.tree.ToggleHiddenFileType(filetree.Unmodified) },
		"keybinding.page-up":                    func() bool { return t.pageUp() },
		"keybinding.page-down":                  func() bool { return t.pageDown() },
	}

	bindingArray := []KeyBinding{}
	actionArray := []keyAction{}

	for keybinding, action := range bindingSettings {
		binding, err := config.GetKeyBinding(keybinding)
		if err != nil {
			panic(fmt.Errorf("setup error during %s: %w", keybinding, err))
			// TODO handle this error
			//return nil
		}
		bindingArray = append(bindingArray, binding)
		actionArray = append(actionArray, action)
	}

	t.inputHandler = func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			t.keyUp()
		case tcell.KeyDown:
			t.keyDown()
		case tcell.KeyRight:
			t.keyRight()
		case tcell.KeyLeft:
			t.keyLeft()
		}

		for idx, binding := range bindingArray {
			if binding.Match(event) {
				actionArray[idx]()
			}
		}
	}

	return t
}

// TODO: do we need all of these?? or is there an alternative API we could use for the wrappers????
func (t *TreeView) getBox() *tview.Box {
	return t.Box
}

func (t *TreeView) getDraw() drawFn {
	return t.Draw
}

func (t *TreeView) getInputWrapper() inputFn {
	return t.InputHandler
}

// Implementation note:
// what do we want here??? a binding object?? yes
func (t *TreeView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return t.inputHandler
}

func (t *TreeView) SetInputHandler(handler func(event *tcell.EventKey, setFocus func(p tview.Primitive))) *TreeView {
	t.inputHandler = handler
	return t
}

func (t *TreeView) WrapInputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return t.Box.WrapInputHandler(t.inputHandler)
}

func (t *TreeView) Focus(delegate func(p tview.Primitive)) {
	t.Box.Focus(delegate)
}

func (t *TreeView) HasFocus() bool {
	return t.Box.HasFocus()
}

// Private helper methods

func (t *TreeView) collapseDir() bool {
	node := t.getAbsPositionNode()
	if node != nil && node.Data.FileInfo.IsDir {
		logrus.Debugf("collapsing node %s", node.Path())
		node.Data.ViewInfo.Collapsed = !node.Data.ViewInfo.Collapsed
		return true
	}
	if node != nil {
		logrus.Debugf("unable to collapse node %s", node.Path())
		logrus.Debugf("  IsDir: %t", node.Data.FileInfo.IsDir)

	} else {
		logrus.Debugf("unable to collapse nil node")
	}
	return false
}

func (t *TreeView) collapseOrExpandAll() bool {
	visitor := func(n *filetree.FileNode) error {
		if n.Data.FileInfo.IsDir {
			n.Data.ViewInfo.Collapsed = t.globalCollapseAll
		}
		return nil
	}

	evaluator := func(n *filetree.FileNode) bool {
		return true
	}
	if err := t.tree.VisitDepthParentFirst(visitor, evaluator); err != nil {
		panic(fmt.Errorf("error callapsing all dir: %w", err))
		// TODO log error here
		//return false
	}


	t.globalCollapseAll = !t.globalCollapseAll
	return true

}

// getAbsPositionNode determines the selected screen cursor's location in the file tree, returning the selected FileNode.
func (t *TreeView) getAbsPositionNode() (node *filetree.FileNode) {
	var visitor func(*filetree.FileNode) error
	var evaluator func(*filetree.FileNode) bool
	var dfsCounter int

	visitor = func(curNode *filetree.FileNode) error {
		if dfsCounter == t.treeIndex {
			node = curNode
		}
		dfsCounter++
		return nil
	}

	evaluator = func(curNode *filetree.FileNode) bool {
		return !curNode.Parent.Data.ViewInfo.Collapsed && !curNode.Data.ViewInfo.Hidden
	}

	err := t.tree.VisitDepthParentFirst(visitor, evaluator)
	if err != nil {
		logrus.Errorf("unable to get node position: %+v", err)
	}

	return node
}

func (t *TreeView) keyDown() bool {
	_, _, _, height := t.Box.GetInnerRect()

	// treeIndex is the index about where we are in the current file
	if t.treeIndex >= t.tree.VisibleSize() {
		return false
	}
	t.treeIndex++
	if (t.treeIndex - t.bufferIndexLowerBound) >= height {
		t.bufferIndexLowerBound++
	}

	logrus.Debugf("  treeIndex: %d", t.treeIndex)
	logrus.Debugf("  bufferIndexLowerBound: %d", t.bufferIndexLowerBound)
	logrus.Debugf("  height: %d", height)

	return true
}

func (t *TreeView) keyUp() bool {
	if t.treeIndex <= 0 {
		return false
	}
	t.treeIndex--
	if t.treeIndex < t.bufferIndexLowerBound {
		t.bufferIndexLowerBound--
	}

	logrus.Debugf("keyUp end at: %s", t.getAbsPositionNode().Path())
	logrus.Debugf("  treeIndex: %d", t.treeIndex)
	logrus.Debugf("  bufferIndexLowerBound: %d", t.bufferIndexLowerBound)
	return true
}

// TODO add regex filtering
func (t *TreeView) keyRight() bool {
	node := t.getAbsPositionNode()

	_, _, _, height := t.Box.GetInnerRect()
	if node == nil {
		return false
	}

	if !node.Data.FileInfo.IsDir {
		return false
	}

	if len(node.Children) == 0 {
		return false
	}

	if node.Data.ViewInfo.Collapsed {
		node.Data.ViewInfo.Collapsed = false
	}

	t.treeIndex++
	if (t.treeIndex - t.bufferIndexLowerBound) >= height {
		t.bufferIndexLowerBound++
	}

	return true
}

func (t *TreeView) keyLeft() bool {
	var visitor func(*filetree.FileNode) error
	var evaluator func(*filetree.FileNode) bool
	var dfsCounter, newIndex int
	//oldIndex := t.treeIndex
	currentNode := t.getAbsPositionNode()

	if currentNode == nil {
		return true
	}
	parentPath := currentNode.Parent.Path()

	visitor = func(curNode *filetree.FileNode) error {
		if strings.Compare(parentPath, curNode.Path()) == 0 {
			newIndex = dfsCounter
		}
		dfsCounter++
		return nil
	}

	evaluator = func(curNode *filetree.FileNode) bool {
		return !curNode.Parent.Data.ViewInfo.Collapsed && !curNode.Data.ViewInfo.Hidden
	}

	err := t.tree.VisitDepthParentFirst(visitor, evaluator)
	if err != nil {
		// TODO: remove this panic
		panic(err)
	}

	t.treeIndex = newIndex
	//moveIndex := oldIndex - newIndex
	if newIndex < t.bufferIndexLowerBound {
		t.bufferIndexLowerBound = t.treeIndex
	}

	return true
}

// TODO make all movement rely on a single function (shouldn't be too dificult really)
func (t *TreeView) pageDown() bool {

	_, _, _, height := t.GetInnerRect()
	visibleSize := t.tree.VisibleSize()
	t.treeIndex = intMin(t.treeIndex+height, visibleSize)
	if t.treeIndex >= t.bufferIndexUpperBound() {
		t.bufferIndexLowerBound = intMin(t.treeIndex, visibleSize-height+1)
	}
	return true
}

func (t *TreeView) pageUp() bool {
	_, _, _, height := t.GetInnerRect()

	t.treeIndex = intMax(0, t.treeIndex-height)
	if t.treeIndex < t.bufferIndexLowerBound {
		t.bufferIndexLowerBound = t.treeIndex
	}

	return true
}

func (t *TreeView) bufferIndexUpperBound() int {
	_, _, _, height := t.Box.GetInnerRect()
	return t.bufferIndexLowerBound + height
}

func (t *TreeView) Draw(screen tcell.Screen) {
	t.Box.Draw(screen)
	selectedIndex := t.treeIndex - t.bufferIndexLowerBound
	x, y, width, height := t.Box.GetInnerRect()
	showAttributes := width > 80 && t.showAttributes
	treeString := t.tree.StringBetween(t.bufferIndexLowerBound, t.bufferIndexUpperBound(), showAttributes)
	lines := strings.Split(treeString, "\n")

	// update the contents
	for yIndex, line := range lines {
		if yIndex >= height {
			break
		}
		//extendedLine := fmt.Sprintf("%s%s",line, strings.Repeat(" ", intMax(0,width - len(line))))
		lineStyle := tcell.StyleDefault
		if yIndex == selectedIndex {
			lineStyle = format.SelectedStyle
		}
		format.PrintLine(screen, line, x, y+yIndex, len(line), tview.AlignLeft, lineStyle)

	}

}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
