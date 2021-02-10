package libpod

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/containers/podman/v2/libpod"
	"github.com/containers/podman/v2/libpod/define"
	"github.com/containers/podman/v2/pkg/api/handlers/utils"
	"github.com/containers/podman/v2/pkg/domain/entities"
	"github.com/containers/podman/v2/pkg/domain/entities/reports"
	"github.com/containers/podman/v2/pkg/domain/filters"
	"github.com/containers/podman/v2/pkg/domain/infra/abi"
	"github.com/containers/podman/v2/pkg/domain/infra/abi/parse"
	"github.com/gorilla/schema"
	"github.com/pkg/errors"
)

func CreateVolume(w http.ResponseWriter, r *http.Request) {
	var (
		volumeOptions []libpod.VolumeCreateOption
		runtime       = r.Context().Value("runtime").(*libpod.Runtime)
		decoder       = r.Context().Value("decoder").(*schema.Decoder)
	)
	query := struct {
	}{
		// override any golang type defaults
	}
	input := entities.VolumeCreateOptions{}
	if err := decoder.Decode(&query, r.URL.Query()); err != nil {
		utils.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest,
			errors.Wrapf(err, "failed to parse parameters for %s", r.URL.String()))
		return
	}
	// decode params from body
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		utils.Error(w, "Something went wrong.", http.StatusInternalServerError, errors.Wrap(err, "Decode()"))
		return
	}

	if len(input.Name) > 0 {
		volumeOptions = append(volumeOptions, libpod.WithVolumeName(input.Name))
	}
	if len(input.Driver) > 0 {
		volumeOptions = append(volumeOptions, libpod.WithVolumeDriver(input.Driver))
	}
	if len(input.Label) > 0 {
		volumeOptions = append(volumeOptions, libpod.WithVolumeLabels(input.Label))
	}
	if len(input.Options) > 0 {
		parsedOptions, err := parse.VolumeOptions(input.Options)
		if err != nil {
			utils.InternalServerError(w, err)
			return
		}
		volumeOptions = append(volumeOptions, parsedOptions...)
	}
	vol, err := runtime.NewVolume(r.Context(), volumeOptions...)
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	inspectOut, err := vol.Inspect()
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	volResponse := entities.VolumeConfigResponse{
		InspectVolumeData: *inspectOut,
	}
	utils.WriteResponse(w, http.StatusCreated, volResponse)
}

func InspectVolume(w http.ResponseWriter, r *http.Request) {
	var (
		runtime = r.Context().Value("runtime").(*libpod.Runtime)
	)
	name := utils.GetName(r)
	vol, err := runtime.GetVolume(name)
	if err != nil {
		utils.VolumeNotFound(w, name, err)
		return
	}
	inspectOut, err := vol.Inspect()
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	volResponse := entities.VolumeConfigResponse{
		InspectVolumeData: *inspectOut,
	}
	utils.WriteResponse(w, http.StatusOK, volResponse)
}

func ListVolumes(w http.ResponseWriter, r *http.Request) {
	var (
		decoder = r.Context().Value("decoder").(*schema.Decoder)
		runtime = r.Context().Value("runtime").(*libpod.Runtime)
	)
	query := struct {
		Filters map[string][]string `schema:"filters"`
	}{
		// override any golang type defaults
	}

	if err := decoder.Decode(&query, r.URL.Query()); err != nil {
		utils.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest,
			errors.Wrapf(err, "failed to parse parameters for %s", r.URL.String()))
		return
	}

	volumeFilters, err := filters.GenerateVolumeFilters(query.Filters)
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}

	vols, err := runtime.Volumes(volumeFilters...)
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	volumeConfigs := make([]*entities.VolumeListReport, 0, len(vols))
	for _, v := range vols {
		inspectOut, err := v.Inspect()
		if err != nil {
			utils.InternalServerError(w, err)
			return
		}
		config := entities.VolumeConfigResponse{
			InspectVolumeData: *inspectOut,
		}
		volumeConfigs = append(volumeConfigs, &entities.VolumeListReport{VolumeConfigResponse: config})
	}
	utils.WriteResponse(w, http.StatusOK, volumeConfigs)
}

func PruneVolumes(w http.ResponseWriter, r *http.Request) {
	reports, err := pruneVolumesHelper(r)
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	utils.WriteResponse(w, http.StatusOK, reports)
}

func pruneVolumesHelper(r *http.Request) ([]*reports.PruneReport, error) {
	var (
		runtime = r.Context().Value("runtime").(*libpod.Runtime)
		decoder = r.Context().Value("decoder").(*schema.Decoder)
	)
	query := struct {
		Filters map[string][]string `schema:"filters"`
	}{
		// override any golang type defaults
	}

	if err := decoder.Decode(&query, r.URL.Query()); err != nil {
		return nil, err
	}

	f := (url.Values)(query.Filters)
	filterFuncs, err := filters.GenerateVolumeFilters(f)
	if err != nil {
		return nil, err
	}

	reports, err := runtime.PruneVolumes(r.Context(), filterFuncs)
	if err != nil {
		return nil, err
	}
	return reports, nil
}
func RemoveVolume(w http.ResponseWriter, r *http.Request) {
	var (
		runtime = r.Context().Value("runtime").(*libpod.Runtime)
		decoder = r.Context().Value("decoder").(*schema.Decoder)
	)
	query := struct {
		Force bool `schema:"force"`
	}{
		// override any golang type defaults
	}

	if err := decoder.Decode(&query, r.URL.Query()); err != nil {
		utils.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest,
			errors.Wrapf(err, "failed to parse parameters for %s", r.URL.String()))
		return
	}
	name := utils.GetName(r)
	vol, err := runtime.LookupVolume(name)
	if err != nil {
		utils.VolumeNotFound(w, name, err)
		return
	}
	if err := runtime.RemoveVolume(r.Context(), vol, query.Force); err != nil {
		if errors.Cause(err) == define.ErrVolumeBeingUsed {
			utils.Error(w, "volumes being used", http.StatusConflict, err)
			return
		}
		utils.InternalServerError(w, err)
		return
	}
	utils.WriteResponse(w, http.StatusNoContent, "")
}

// ExistsVolume check if a volume exists
func ExistsVolume(w http.ResponseWriter, r *http.Request) {
	runtime := r.Context().Value("runtime").(*libpod.Runtime)
	name := utils.GetName(r)

	ic := abi.ContainerEngine{Libpod: runtime}
	report, err := ic.VolumeExists(r.Context(), name)
	if err != nil {
		utils.Error(w, "Something went wrong.", http.StatusInternalServerError, err)
		return
	}
	if !report.Value {
		utils.Error(w, "volume not found", http.StatusNotFound, define.ErrNoSuchVolume)
		return
	}
	utils.WriteResponse(w, http.StatusNoContent, "")
}